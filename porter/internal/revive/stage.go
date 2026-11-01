package revive

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	porterenroll "valise/porter/internal/enroll"
)

// Inspect reports facts about the target for planning. It only reads.
func Inspect(ctx context.Context, e *porterenroll.Env, s Store, p revive.InspectParams) (*revive.InspectResult, error) {
	e = e.For(&p.Profile)
	res := &revive.InspectResult{Home: e.Home, OS: runtime.GOOS, Arch: runtime.GOARCH, Tools: map[string]bool{}, PendingOps: []string{}}
	if p.Profile.Detect != nil {
		for _, c := range p.Profile.Detect.StateDirCandidates {
			if dir, ok := e.Expand(c, nil); ok {
				res.StateDir = filepath.Clean(dir)
				break
			}
		}
	}
	var roots []enroll.Root
	for _, rp := range p.Roots {
		f := revive.RootFacts{ID: rp.ID, Path: rp.Path}
		st, err := os.Lstat(rp.Path)
		f.Exists = err == nil && st.IsDir()
		f.Symlinked = isSymlinked(rp.Path)
		parent := filepath.Dir(rp.Path)
		f.FreeBytes, _ = statfsFree(nearestExisting(parent))
		f.SameFS = !f.Exists || sameFS(parent, rp.Path)
		if f.Exists {
			filepath.WalkDir(rp.Path, func(_ string, d fs.DirEntry, err error) error {
				if err == nil && d.Type().IsRegular() {
					if info, err := d.Info(); err == nil {
						f.UsedBytes += info.Size()
					}
				}
				return nil
			})
		}
		res.Roots = append(res.Roots, f)
		roots = append(roots, enroll.Root{ID: rp.ID, Path: rp.Path, Exists: f.Exists})
	}
	for _, t := range []string{"npm", "git", "uv", "pipx", "systemctl"} {
		_, err := e.LookPath(t)
		res.Tools[t] = err == nil
	}
	res.Provenance = porterenroll.Provenance(ctx, e, &p.Profile, roots)
	if p.Provenance.NPM != nil && p.Provenance.NPM.Registry != "" && res.Tools["npm"] {
		out, err := e.Run(ctx, 60*time.Second, "", "npm", "view", p.Provenance.NPM.Package+"@"+p.Provenance.NPM.Version,
			"dist.integrity", "--registry", p.Provenance.NPM.Registry)
		if err == nil && out.Code == 0 {
			res.RegistryCheck = strings.TrimSpace(string(out.Stdout))
		}
	}
	if p.Profile.Service != nil {
		for _, u := range p.Profile.Service.Candidates {
			st := enroll.ServiceState{Scope: u.Scope, Unit: u.Unit}
			if unitExists(ctx, e, u.Unit) {
				st.LoadState = "loaded"
			}
			st.Active = unitState(ctx, e, u.Unit)
			res.Service = append(res.Service, st)
		}
	}
	for _, en := range p.Profile.Entries {
		res.Listening = append(res.Listening, enroll.EntryState{ID: en.ID, Host: en.Host, Port: en.Port, Listening: dialOK(en.Host, en.Port)})
	}
	if enroll.ValidInstanceID(p.InstanceID) == nil {
		res.Record, _ = porterenroll.ReadRecord(e.Home, p.InstanceID)
	}
	res.Overlapping = overlapping(e.Home, p.InstanceID, p.Roots)
	pend, err := s.Pending()
	if err != nil {
		return nil, err
	}
	for _, j := range pend {
		res.PendingOps = append(res.PendingOps, j.Plan.OpID)
	}
	unit := ""
	if p.Profile.Service != nil && len(p.Profile.Service.Candidates) > 0 {
		unit = p.Profile.Service.Candidates[0].Unit
	}
	var paths []string
	for _, f := range res.Roots {
		if f.Exists {
			paths = append(paths, f.Path)
		}
	}
	res.Busy = busyProcesses(paths, unit)
	return res, nil
}

func nearestExisting(p string) string {
	for {
		if _, err := os.Stat(p); err == nil || p == filepath.Dir(p) {
			return p
		}
		p = filepath.Dir(p)
	}
}

// isSymlinked reports whether the root itself is a symlink. Symlinked
// ancestors (e.g. /var on macOS, /home on a data disk) are harmless: the
// swap renames entries inside the root's parent directory.
func isSymlinked(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

func device(p string) (uint64, bool) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(sys.Dev), true
}

func sameFS(a, b string) bool {
	da, ok1 := device(a)
	db, ok2 := device(b)
	return ok1 && ok2 && da == db
}

func statfsFree(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// swapRoots are the plan's roots that are not nested in another root.
func swapRoots(p revive.Plan) []revive.RootPlan {
	var out []revive.RootPlan
	for _, r := range p.Roots {
		if r.SwapRoot == "" {
			out = append(out, r)
		}
	}
	return out
}

// StageBegin validates the plan, writes the PLANNED journal and creates the
// stage directories next to the live ones.
func StageBegin(ctx context.Context, e *porterenroll.Env, s Store, p revive.Plan) (*revive.Journal, error) {
	if !revive.ValidOpID(p.OpID) {
		return nil, valise.New("op_invalid", "操作 id 无效", "重新运行 valise revive")
	}
	switch p.Mode {
	case revive.ModeRollback, revive.ModeMigrate, revive.ModeClone:
	default:
		return nil, valise.New("mode_invalid", "未知的复活模式："+p.Mode, "可选 rollback、migrate、clone")
	}
	if enroll.ValidInstanceID(p.InstanceID) != nil {
		return nil, valise.New("instance_id_invalid", "实例 id 无效", "使用合法的实例 id")
	}
	pend, err := s.Pending()
	if err != nil {
		return nil, err
	}
	for _, j := range pend {
		if j.Plan.InstanceID == p.InstanceID {
			return nil, valise.New("op_pending", "实例 "+p.InstanceID+" 有未完成的操作 "+j.Plan.OpID,
				"先处理它：valise revive resume <服务器> "+j.Plan.OpID+" --continue 或 --back")
		}
	}
	j := &revive.Journal{Plan: p}
	for _, r := range swapRoots(p) {
		if !filepath.IsAbs(r.Path) || filepath.Clean(r.Path) != r.Path || r.Path == "/" || r.Path == e.Home {
			return nil, valise.New("root_invalid", "目标路径无效："+r.Path, "检查 --root 参数")
		}
		if isSymlinked(r.Path) {
			return nil, valise.New("root_symlinked", "目标目录 "+r.Path+" 本身是符号链接，v0.1 不支持在链接背后交换目录",
				"链接通常由其他工具管理；请改用真实目录，或手动处理后再复活")
		}
		jr := revive.JournalRoot{ID: r.ID, Live: r.Path,
			Stage: sibling(r.Path, "stage", p.OpID, r.ID), Prev: sibling(r.Path, "prev", p.OpID, r.ID), Failed: sibling(r.Path, "failed", p.OpID, r.ID)}
		if st, err := os.Lstat(r.Path); err == nil && st.IsDir() {
			jr.LiveExisted = true
		}
		if err := os.MkdirAll(filepath.Dir(r.Path), 0o700); err != nil {
			return nil, err
		}
		if !sameFS(filepath.Dir(r.Path), nearestExisting(r.Path)) {
			return nil, valise.New("not_same_fs", r.Path+" 与它的父目录不在同一个文件系统", "暂存目录必须与活动目录在同一文件系统，才能原子交换")
		}
		j.Roots = append(j.Roots, jr)
	}
	for _, jr := range j.Roots {
		if err := os.Mkdir(jr.Stage, 0o700); err != nil {
			return nil, valise.New("stage_exists", "无法创建暂存目录 "+jr.Stage, "检查是否有残留的同名目录").WithDetail(err.Error())
		}
	}
	if err := s.Set(j, revive.StatePlanned); err != nil {
		return nil, err
	}
	return j, nil
}

// stageDest maps a tar name "<swap root>/<rel>" to a path in the stage.
func stageDest(j *revive.Journal, name string) (string, error) {
	root, rel, ok := strings.Cut(name, "/")
	if !ok || rel == "" || strings.HasPrefix(rel, "/") || filepath.Clean(rel) != filepath.FromSlash(rel) || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("unsafe entry %q", name)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return "", fmt.Errorf("unsafe entry %q", name)
		}
	}
	for _, r := range j.Roots {
		if r.ID == root {
			return filepath.Join(r.Stage, filepath.FromSlash(rel)), nil
		}
	}
	return "", fmt.Errorf("unknown root in %q", name)
}

// mkdirNoFollow creates dir and its parents below base, refusing to pass
// through any symlink.
func mkdirNoFollow(base, dir string) error {
	rel, err := filepath.Rel(base, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("outside stage: %s", dir)
	}
	cur := base
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if seg == "." || seg == "" {
			continue
		}
		cur = filepath.Join(cur, seg)
		st, err := os.Lstat(cur)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(cur, 0o700); err != nil {
				return err
			}
		case err != nil:
			return err
		case !st.IsDir():
			return fmt.Errorf("not a directory (symlink?): %s", cur)
		}
	}
	return nil
}

func rootOf(j *revive.Journal, dest string) string {
	for _, r := range j.Roots {
		if strings.HasPrefix(dest, r.Stage+string(filepath.Separator)) {
			return r.Stage
		}
	}
	return ""
}

// StageWrite reads the tar stream produced by the client into the stage.
func StageWrite(s Store, op string, r io.Reader) (files int, err error) {
	j, err := s.Load(op)
	if err != nil {
		return 0, err
	}
	if j.State != revive.StatePlanned || j.StageVerified {
		return 0, errors.New("stage is not open for writing")
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return files, err
		}
		dest, err := stageDest(j, h.Name)
		if err != nil {
			return files, err
		}
		base := rootOf(j, dest)
		if err := mkdirNoFollow(base, filepath.Dir(dest)); err != nil {
			return files, err
		}
		switch h.Typeflag {
		case tar.TypeReg:
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
			if err != nil {
				return files, err
			}
			n, err := io.Copy(f, tr)
			if err == nil && n != h.Size {
				err = io.ErrUnexpectedEOF
			}
			if err == nil {
				err = f.Sync()
			}
			f.Close()
			if err != nil {
				return files, err
			}
			if err := os.Chmod(dest, fs.FileMode(h.Mode).Perm()); err != nil {
				return files, err
			}
			if err := os.Chtimes(dest, h.ModTime, h.ModTime); err != nil {
				return files, err
			}
			files++
		case tar.TypeSymlink:
			if err := os.Symlink(h.Linkname, dest); err != nil {
				return files, err
			}
		default:
			return files, fmt.Errorf("unsupported tar entry type %c", h.Typeflag)
		}
	}
}

// StageVerify checks every staged file against the manifest, runs SQLite
// integrity_check on databases and makes secrets and unclassified files
// 0600.
func StageVerify(s Store, p revive.StageVerifyParams) (*revive.StageVerifyResult, error) {
	j, err := s.Load(p.OpID)
	if err != nil {
		return nil, err
	}
	if j.State != revive.StatePlanned {
		return nil, valise.New("op_state", "操作已经越过暂存阶段", "用 valise revive resume 处理")
	}
	res := &revive.StageVerifyResult{Problems: []string{}}
	want := map[string]bool{}
	for _, m := range p.Manifest {
		dest, err := stageDest(j, m.Path)
		if err != nil {
			res.Problems = append(res.Problems, err.Error())
			continue
		}
		want[dest] = true
		st, err := os.Lstat(dest)
		if err != nil || !st.Mode().IsRegular() {
			res.Problems = append(res.Problems, m.Path+"：暂存目录中缺少该文件")
			continue
		}
		f, err := os.Open(dest)
		if err != nil {
			res.Problems = append(res.Problems, m.Path+"：无法读取")
			continue
		}
		h := sha256.New()
		n, _ := io.Copy(h, f)
		f.Close()
		if n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
			res.Problems = append(res.Problems, m.Path+"：内容与快照不一致")
			continue
		}
		if vault.Sensitive("", m.Category) {
			os.Chmod(dest, 0o600)
		}
		if m.SQLite {
			if err := integrity(dest); err != nil {
				res.Problems = append(res.Problems, m.Path+"：SQLite 完整性检查失败："+err.Error())
			}
		}
		res.Files++
		res.Bytes += n
	}
	for _, r := range j.Roots {
		filepath.WalkDir(r.Stage, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() && !want[path] {
				res.Problems = append(res.Problems, "暂存目录中有清单之外的文件："+path)
			}
			return nil
		})
	}
	if len(res.Problems) == 0 {
		j.StageVerified = true
		if err := s.Save(j); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func integrity(path string) error {
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}).String())
	if err != nil {
		return err
	}
	defer db.Close()
	var r string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&r); err != nil {
		return err
	}
	if r != "ok" {
		return errors.New(r)
	}
	return nil
}

// classifierFor builds the classifier of a plan.
func classifierFor(p revive.Plan) *profile.Classifier {
	return profile.NewClassifier(&p.Profile, p.Overrides)
}

// overlapping lists other enrolled, not yet superseded instances with a root
// equal to, inside or containing one of the planned roots.
func overlapping(home, self string, roots []revive.RootPlan) []string {
	recs, _ := porterenroll.ListRecords(home)
	var out []string
	for _, r := range recs {
		if r.InstanceID == self || r.Superseded != nil {
			continue
		}
	match:
		for _, a := range r.Roots {
			for _, b := range roots {
				if within(a, b.Path) || within(b.Path, a) {
					out = append(out, r.InstanceID)
					break match
				}
			}
		}
	}
	return out
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
