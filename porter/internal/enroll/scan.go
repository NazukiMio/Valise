package enroll

import (
	"context"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
)

const (
	discoverTimeout = 15 * time.Second
	systemdTimeout  = 5 * time.Second
	dialTimeout     = time.Second
	maxUnreadable   = 50
)

// expandArgv expands variables in every argv element.
func (e *Env) expandArgv(argv []string, vars map[string]string) ([]string, bool) {
	out := make([]string, len(argv))
	for i, a := range argv {
		v, ok := e.Expand(a, vars)
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// Scan resolves roots, classifies every file, and records provenance,
// services and entries. It only reads; nothing on the server is modified.
func Scan(ctx context.Context, e *Env, params enroll.ScanParams, previous *enroll.Record) (*enroll.Report, error) {
	p := &params.Profile
	if err := enroll.ValidInstanceID(params.InstanceID); err != nil {
		return nil, valise.New("instance_id_invalid", err.Error(), "用 --id 指定一个由小写字母、数字和连字符组成的 id")
	}
	if st, err := os.Stat(params.StateDir); err != nil || !st.IsDir() || !filepath.IsAbs(params.StateDir) {
		return nil, valise.New("state_dir_missing", "状态目录不存在："+params.StateDir, "重新运行登记，让 Valise 重新探测")
	}
	for _, o := range params.Overrides {
		if err := o.Validate(p); err != nil {
			return nil, valise.New("override_invalid", "归类指令无效："+err.Error(), "格式为 --assign <root>:<glob>=<类别>")
		}
	}
	r := &enroll.Report{
		InstanceID: params.InstanceID, ProfileID: p.ID, ProfileVersion: p.ProfileVersion, ProfileStatus: p.Status,
		Categories: map[string]enroll.Tally{}, Secrets: []enroll.FileRef{}, SQLite: []enroll.SQLiteFile{},
		UnclassGroups: []enroll.UnclassifiedGroup{}, Service: []enroll.ServiceState{}, Entries: []enroll.EntryState{},
		Overrides: params.Overrides, ScannedAt: time.Now().UTC(), Previous: previous,
	}
	r.Warnings = append(r.Warnings, e.Warnings...)
	r.Roots = resolveRoots(ctx, e, p, params.StateDir, &r.Warnings)
	classify(r, p, params.Overrides)
	r.Provenance = Provenance(ctx, e, p, r.Roots)
	r.Service = services(ctx, e, p, &r.Warnings)
	r.Entries = entries(p)
	if previous != nil {
		r.Changes = enroll.Diff(*previous, enroll.RecordFrom(r, r.ScannedAt))
	}
	return r, nil
}

func resolveRoots(ctx context.Context, e *Env, p *profile.Profile, stateDir string, warns *[]string) []enroll.Root {
	roots := []enroll.Root{resolved(enroll.Root{ID: profile.StateRoot, Path: stateDir, Source: "detect", Exists: true})}
	vars := map[string]string{"STATE_DIR": stateDir}
	for _, rt := range p.Roots {
		res := enroll.Root{ID: rt.ID}
		if len(rt.DiscoverArgv) > 0 {
			if path, ok := discover(ctx, e, rt.DiscoverArgv, vars, stateDir); ok {
				res.Path, res.Source = path, "discover"
			} else {
				*warns = append(*warns, "无法通过 harness 命令读出 "+rt.ID+" 的实际路径，改用 profile 中的默认路径")
			}
		}
		if res.Path == "" {
			path, ok := e.Expand(rt.Default, vars)
			if !ok {
				*warns = append(*warns, rt.ID+" 的默认路径依赖未设置的环境变量，已跳过")
				continue
			}
			res.Path, res.Source = filepath.Clean(path), "default"
		}
		st, err := os.Stat(res.Path)
		res.Exists = err == nil && st.IsDir()
		roots = append(roots, resolved(res))
	}
	return roots
}

// resolved follows symlinks in a root's own path; symlinks inside the root
// are never followed.
func resolved(r enroll.Root) enroll.Root {
	if real, err := filepath.EvalSymlinks(r.Path); err == nil && real != r.Path {
		r.Resolved = real
	}
	return r
}

// discover runs discover_argv; stdout must be exactly one absolute path.
func discover(ctx context.Context, e *Env, argv []string, vars map[string]string, dir string) (string, bool) {
	args, ok := e.expandArgv(argv, vars)
	if !ok {
		return "", false
	}
	out, err := e.Run(ctx, discoverTimeout, dir, args...)
	if err != nil || out.Code != 0 {
		return "", false
	}
	path := strings.TrimSpace(string(out.Stdout))
	if !filepath.IsAbs(path) || strings.Contains(path, "\n") {
		return "", false
	}
	return filepath.Clean(path), true
}

// classify walks every existing root. A file belongs only to the deepest
// root containing it.
func classify(r *enroll.Report, p *profile.Profile, overrides []profile.Override) {
	c := profile.NewClassifier(p, overrides)
	groups := map[[2]string]*enroll.UnclassifiedGroup{}
	sqlite := map[enroll.FileRef]*enroll.SQLiteFile{}
	sidecars := map[enroll.FileRef][]string{}
	for _, root := range r.Roots {
		if !root.Exists {
			continue
		}
		var nested []string
		for _, other := range r.Roots {
			if other.ID != root.ID && other.Exists && strings.HasPrefix(other.Dir(), root.Dir()+string(filepath.Separator)) {
				nested = append(nested, other.Dir())
			}
		}
		_ = filepath.WalkDir(root.Dir(), func(path string, d fs.DirEntry, err error) error {
			rel, _ := filepath.Rel(root.Dir(), path)
			rel = filepath.ToSlash(rel)
			if err != nil {
				if len(r.Unreadable) < maxUnreadable {
					r.Unreadable = append(r.Unreadable, enroll.FileRef{Root: root.ID, Path: rel})
				}
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				for _, n := range nested {
					if path == n {
						return fs.SkipDir
					}
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				r.Symlinks++
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // sockets, fifos, devices
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			ref := enroll.FileRef{Root: root.ID, Path: rel}
			if db, ok := c.Sidecar(root.ID, rel); ok {
				dbRef := enroll.FileRef{Root: root.ID, Path: db}
				sidecars[dbRef] = append(sidecars[dbRef], strings.TrimPrefix(rel, db))
				return nil
			}
			m := c.Classify(root.ID, rel)
			if m.Category == "" {
				r.Unclassified.Files++
				r.Unclassified.Bytes += info.Size()
				key := [2]string{root.ID, groupPrefix(rel)}
				g := groups[key]
				if g == nil {
					g = &enroll.UnclassifiedGroup{Root: root.ID, Prefix: key[1]}
					groups[key] = g
				}
				g.Files++
				g.Bytes += info.Size()
				return nil
			}
			t := r.Categories[m.Category]
			t.Files++
			t.Bytes += info.Size()
			r.Categories[m.Category] = t
			if m.Category == profile.CategorySecrets {
				r.Secrets = append(r.Secrets, ref)
			}
			if decl, ok := c.SQLite(root.ID, rel); ok {
				sqlite[ref] = &enroll.SQLiteFile{FileRef: ref, Category: m.Category, SecretTables: decl.SecretTables}
			}
			return nil
		})
	}
	for ref, s := range sqlite {
		sort.Strings(sidecars[ref])
		s.Sidecars = sidecars[ref]
		r.SQLite = append(r.SQLite, *s)
	}
	sort.Slice(r.SQLite, func(i, j int) bool { return r.SQLite[i].Root+r.SQLite[i].Path < r.SQLite[j].Root+r.SQLite[j].Path })
	for _, g := range groups {
		r.UnclassGroups = append(r.UnclassGroups, *g)
	}
	sort.Slice(r.UnclassGroups, func(i, j int) bool {
		a, b := r.UnclassGroups[i], r.UnclassGroups[j]
		return a.Root+"/"+a.Prefix < b.Root+"/"+b.Prefix
	})
	sort.Slice(r.Secrets, func(i, j int) bool { return r.Secrets[i].Root+r.Secrets[i].Path < r.Secrets[j].Root+r.Secrets[j].Path })
}

// groupPrefix keeps the first two path segments ("a/b/c/d" -> "a/b/").
func groupPrefix(rel string) string {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) <= 2 {
		if len(parts) == 2 {
			return parts[0] + "/"
		}
		return rel
	}
	return parts[0] + "/" + parts[1] + "/"
}

func services(ctx context.Context, e *Env, p *profile.Profile, warns *[]string) []enroll.ServiceState {
	out := []enroll.ServiceState{}
	if p.Service == nil || len(p.Service.Candidates) == 0 {
		return out
	}
	for _, u := range p.Service.Candidates {
		s := enroll.ServiceState{Scope: u.Scope, Unit: u.Unit}
		res, err := e.Run(ctx, systemdTimeout, "", "systemctl", "--user", "show", u.Unit,
			"-p", "LoadState", "-p", "ActiveState", "-p", "UnitFileState")
		if err != nil {
			*warns = append(*warns, "无法查询 systemd 用户服务："+err.Error())
			out = append(out, s)
			continue
		}
		for _, line := range strings.Split(string(res.Stdout), "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
			switch k {
			case "LoadState":
				s.LoadState = v
			case "ActiveState":
				s.Active = v
			case "UnitFileState":
				s.Enabled = v
			}
		}
		out = append(out, s)
	}
	return out
}

func entries(p *profile.Profile) []enroll.EntryState {
	out := []enroll.EntryState{}
	for _, en := range p.Entries {
		s := enroll.EntryState{ID: en.ID, Host: en.Host, Port: en.Port}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(en.Host, strconv.Itoa(en.Port)), dialTimeout)
		if err == nil {
			s.Listening = true
			conn.Close()
		}
		out = append(out, s)
	}
	return out
}
