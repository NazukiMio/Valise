package revive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	porterenroll "valise/porter/internal/enroll"
)

const npmTimeout = 20 * time.Minute

// Runner executes journaled swaps.
type Runner struct {
	Env   *porterenroll.Env
	Store Store
	// Audit appends an audit entry (op, instance, result).
	Audit func(op, instance, result string)
	// Timing overrides health check timing (tests).
	Timing HealthTiming
}

func (r *Runner) set(j *revive.Journal, state string) error { return r.Store.Set(j, state) }

// Swap runs a staged, verified plan to the end (or back).
func (r *Runner) Swap(ctx context.Context, op string) (*revive.SwapResult, error) {
	j, err := r.Store.Load(op)
	if err != nil {
		return nil, err
	}
	if j.State != revive.StatePlanned || !j.StageVerified {
		return nil, valise.New("op_state", "暂存尚未校验通过，不能交换", "先完成暂存与校验")
	}
	return r.advance(ctx, j)
}

// Resume continues or backs out an unfinished operation (crash recovery).
func (r *Runner) Resume(ctx context.Context, p revive.ResumeParams) (*revive.SwapResult, error) {
	j, err := r.Store.Load(p.OpID)
	if err != nil {
		return nil, err
	}
	if revive.Final(j.State) {
		return nil, valise.New("op_finished", "操作已经结束（"+j.State+"）", "无需处理")
	}
	switch p.Action {
	case "continue":
		if j.State == revive.StatePlanned && !j.StageVerified {
			return nil, valise.New("op_state", "暂存没有校验通过，不能继续", "改用 --back 退回")
		}
		if j.State == revive.StateRollingBack {
			return r.rollback(ctx, j, "继续完成退回")
		}
		return r.advance(ctx, j)
	case "back":
		return r.back(ctx, j, "用户选择退回")
	}
	return nil, valise.New("resume_action_invalid", "未知的处理方式", "使用 --continue 或 --back")
}

// advance moves the journal forward from its current state.
func (r *Runner) advance(ctx context.Context, j *revive.Journal) (*revive.SwapResult, error) {
	res := &revive.SwapResult{OpID: j.Plan.OpID}
	fail := func(err error) (*revive.SwapResult, error) {
		j.Error = err.Error()
		return r.back(ctx, j, err.Error())
	}
	for {
		switch j.State {
		case revive.StatePlanned:
			if j.Plan.Unit != "" && unitExists(ctx, r.Env, j.Plan.Unit) {
				j.ServiceWasActive = unitState(ctx, r.Env, j.Plan.Unit) == "active"
				if j.ServiceWasActive {
					if err := StopUnit(ctx, r.Env, j.Plan.Unit); err != nil {
						return fail(err)
					}
				}
			} else {
				for _, en := range j.Plan.Profile.Entries {
					if dialOK(en.Host, en.Port) {
						return fail(valise.New("harness_running", fmt.Sprintf("入口 %s:%d 仍在监听，harness 可能在前台运行", en.Host, en.Port), "先手动停止 harness，再重试"))
					}
				}
			}
			if err := r.set(j, revive.StateStopped); err != nil {
				return nil, err
			}
		case revive.StateStopped:
			var lives []string
			for _, jr := range j.Roots {
				if exists(jr.Live) {
					lives = append(lives, jr.Live)
				}
			}
			if busy := busyProcesses(lives, ""); len(busy) > 0 {
				var desc []string
				for _, b := range busy {
					desc = append(desc, fmt.Sprintf("pid %d：%s", b.PID, b.Command))
				}
				return fail(valise.New("root_busy", "服务停止后，仍有进程在使用状态目录", "先停止这些进程再重试（Valise 不会替你结束它们）").
					WithDetail(strings.Join(desc, "\n")))
			}
			if j.Plan.Carry {
				n, err := r.carry(j)
				if err != nil {
					return fail(err)
				}
				res.Carried = n
			}
			for _, jr := range j.Roots {
				if jr.LiveExisted && !exists(jr.Prev) {
					if err := os.Rename(jr.Live, jr.Prev); err != nil {
						return fail(err)
					}
				}
			}
			if err := r.set(j, revive.StateLiveMoved); err != nil {
				return nil, err
			}
		case revive.StateLiveMoved:
			for _, jr := range j.Roots {
				if !exists(jr.Stage) && exists(jr.Live) {
					continue // already moved (resumed after a crash)
				}
				if err := r.placeAt(j, jr, jr.Stage); err != nil {
					return fail(err)
				}
			}
			if err := r.set(j, revive.StageMoved); err != nil {
				return nil, err
			}
		case revive.StageMoved:
			if err := r.switchBody(ctx, j); err != nil {
				return fail(err)
			}
			if err := r.set(j, revive.StateBodySwitched); err != nil {
				return nil, err
			}
		case revive.StateBodySwitched:
			if j.Plan.InstallService && !j.ServiceInstalled {
				if err := r.installService(ctx, j); err != nil {
					return fail(err)
				}
			}
			if j.Plan.Unit == "" {
				if err := r.set(j, revive.StateRestored); err != nil {
					return nil, err
				}
				continue
			}
			if err := StartUnit(ctx, r.Env, j.Plan.Unit); err != nil {
				return fail(err)
			}
			if err := r.set(j, revive.StateStarted); err != nil {
				return nil, err
			}
		case revive.StateStarted:
			log, ok := Health(ctx, r.Env, j.Plan.Profile, j.Plan.Unit, r.Timing)
			res.HealthLog = log
			if !ok {
				return r.rollback(ctx, j, "健康检查没有通过")
			}
			if err := r.set(j, revive.StateHealthy); err != nil {
				return nil, err
			}
		case revive.StateHealthy, revive.StateRestored:
			if j.Plan.Record != nil {
				rec := *j.Plan.Record
				porterenroll.Refresh(ctx, r.Env, &j.Plan.Profile, &rec)
				if _, err := porterenroll.WriteRecord(r.Env.Home, rec, j.Plan.Profile); err != nil {
					return nil, err
				}
				for _, id := range j.Plan.Supersedes {
					sup := enroll.Supersession{By: rec.InstanceID, OpID: j.Plan.OpID, At: time.Now().UTC()}
					if err := porterenroll.MarkSuperseded(r.Env.Home, id, sup); err != nil {
						return nil, err
					}
				}
			}
			r.Store.Finish(j)
			r.audit(j, "ok")
			res.State = j.State
			for _, jr := range j.Roots {
				if exists(jr.Prev) {
					res.Prev = append(res.Prev, jr.Prev)
				}
			}
			res.Intruders = j.Intruders
			if j.State == revive.StateHealthy {
				res.Message = "复活完成，服务健康"
			} else {
				res.Message = "已恢复文件；这个 harness 没有服务单元，Valise 不会启动它"
			}
			return res, nil
		default:
			return nil, valise.New("op_state", "无法从状态 "+j.State+" 继续", "改用 --back 退回")
		}
	}
}

// back undoes an operation according to how far it got.
func (r *Runner) back(ctx context.Context, j *revive.Journal, why string) (*revive.SwapResult, error) {
	switch j.State {
	case revive.StatePlanned, revive.StateStopped:
		// The live directories were never touched. The stage is kept and
		// listed; only `valise prev drop` removes it.
		return r.finishBack(ctx, j, why)
	case revive.StateLiveMoved:
		for _, jr := range j.Roots {
			if jr.LiveExisted && exists(jr.Prev) {
				if err := r.placeAt(j, jr, jr.Prev); err != nil {
					return nil, err
				}
			}
		}
		return r.finishBack(ctx, j, why)
	}
	return r.rollback(ctx, j, why)
}

// rollback is the failure path after the new state is in place
// (docs/ARCHITECTURE.md section 8.2 step 5).
func (r *Runner) rollback(ctx context.Context, j *revive.Journal, why string) (*revive.SwapResult, error) {
	if j.State != revive.StateRollingBack {
		if err := r.set(j, revive.StateRollingBack); err != nil {
			return nil, err
		}
	}
	if j.Plan.Unit != "" && unitExists(ctx, r.Env, j.Plan.Unit) {
		StopUnit(ctx, r.Env, j.Plan.Unit)
	}
	for _, jr := range j.Roots {
		if exists(jr.Live) && !exists(jr.Failed) && (exists(jr.Prev) || !jr.LiveExisted) {
			if err := os.Rename(jr.Live, jr.Failed); err != nil {
				return nil, err
			}
		}
	}
	// Body directories travelled into the new live dir: bring them back.
	for i, m := range j.BodyMoves {
		if !m.Moved {
			continue
		}
		jr := journalRoot(j, m.Root)
		from, to := filepath.Join(jr.Failed, m.Rel), filepath.Join(jr.Prev, m.Rel)
		if exists(from) && !exists(to) {
			if err := os.Rename(from, to); err != nil {
				return nil, err
			}
		}
		j.BodyMoves[i].Moved = false
		r.Store.Save(j)
	}
	if j.NPMInstalled && j.NPMOldVersion != "" && j.NPMOldVersion != j.Plan.Body.Version {
		r.npmInstall(ctx, j.Plan.Body.Package, j.NPMOldVersion, j.Plan.Body.Registry)
	}
	for _, jr := range j.Roots {
		if jr.LiveExisted && exists(jr.Prev) {
			if err := r.placeAt(j, jr, jr.Prev); err != nil {
				return nil, err
			}
		}
	}
	return r.finishBack(ctx, j, why)
}

// placeAt renames from to the live path. If something recreated the live
// path in the meantime (another process of the harness writing by path), it
// is moved aside to .valise-intruder-<op>-<root>, never deleted. The live
// path is checked afterwards so a skipped rename can never be reported as
// success.
func (r *Runner) placeAt(j *revive.Journal, jr revive.JournalRoot, from string) error {
	if exists(jr.Live) {
		aside := sibling(jr.Live, "intruder", j.Plan.OpID, jr.ID)
		for i := 2; exists(aside); i++ {
			aside = sibling(jr.Live, fmt.Sprintf("intruder%d", i), j.Plan.OpID, jr.ID)
		}
		if err := os.Rename(jr.Live, aside); err != nil {
			return err
		}
		j.Intruders = append(j.Intruders, aside)
		if err := r.Store.Save(j); err != nil {
			return err
		}
	}
	if err := os.Rename(from, jr.Live); err != nil {
		return valise.New("swap_rename_failed", "无法把 "+from+" 放到 "+jr.Live, "不要手动改动这些目录；用 valise revive resume 重试").WithDetail(err.Error())
	}
	if exists(from) || !exists(jr.Live) {
		return valise.New("swap_rename_unverified", "改名后核实失败："+jr.Live, "不要手动改动这些目录；用 valise revive resume 重试")
	}
	return nil
}

func (r *Runner) finishBack(ctx context.Context, j *revive.Journal, why string) (*revive.SwapResult, error) {
	res := &revive.SwapResult{OpID: j.Plan.OpID, State: revive.StateRolledBack}
	if j.Plan.Unit != "" && j.ServiceWasActive {
		if err := StartUnit(ctx, r.Env, j.Plan.Unit); err == nil {
			_, ok := Health(ctx, r.Env, j.Plan.Profile, j.Plan.Unit, r.Timing)
			if !ok {
				why += "；退回后原实例的健康检查也没有通过"
			}
		}
	}
	if j.Plan.Unit != "" {
		res.Logs = recentLogs(ctx, r.Env, j.Plan.Unit)
	}
	if err := r.set(j, revive.StateRolledBack); err != nil {
		return nil, err
	}
	r.Store.Finish(j)
	r.audit(j, "rolled_back")
	res.Message = "已退回到交换前的状态：" + why
	for _, jr := range j.Roots {
		for _, p := range []string{jr.Failed, jr.Stage} {
			if exists(p) {
				res.Prev = append(res.Prev, p)
			}
		}
	}
	res.Intruders = j.Intruders
	return res, nil
}

func (r *Runner) audit(j *revive.Journal, result string) {
	if r.Audit != nil {
		r.Audit(j.Plan.OpID, j.Plan.InstanceID, result)
	}
}

func journalRoot(j *revive.Journal, id string) revive.JournalRoot {
	for _, jr := range j.Roots {
		if jr.ID == id {
			return jr
		}
	}
	return revive.JournalRoot{}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// switchBody keeps, moves or installs the body.
func (r *Runner) switchBody(ctx context.Context, j *revive.Journal) error {
	b := j.Plan.Body
	switch b.Action {
	case revive.BodyKeep, "":
		return nil
	case revive.BodyMoveDirs:
		if len(j.BodyMoves) == 0 {
			for _, d := range b.Dirs {
				root, rel, _ := strings.Cut(d, "/")
				j.BodyMoves = append(j.BodyMoves, revive.BodyMove{Root: root, Rel: rel})
			}
		}
		for i, m := range j.BodyMoves {
			if m.Moved {
				continue
			}
			jr := journalRoot(j, m.Root)
			from, to := filepath.Join(jr.Prev, m.Rel), filepath.Join(jr.Live, m.Rel)
			if !exists(from) {
				return valise.New("body_missing", "交换前的状态中没有身体目录 "+m.Rel, "身体不在原位，无法保持不变")
			}
			if exists(to) {
				continue // the snapshot (raw tier) brought its own body
			}
			if err := os.Rename(from, to); err != nil {
				return err
			}
			j.BodyMoves[i].Moved = true
			if err := r.Store.Save(j); err != nil {
				return err
			}
		}
		return nil
	case revive.BodyNPMInstall:
		if !j.NPMInstalled {
			j.NPMOldVersion = installedNPMVersion(ctx, r.Env, b.Package)
			if err := r.Store.Save(j); err != nil {
				return err
			}
			if err := r.npmInstall(ctx, b.Package, b.Version, b.Registry); err != nil {
				return err
			}
			j.NPMInstalled = true
			if v := installedNPMVersion(ctx, r.Env, b.Package); v != b.Version {
				return valise.New("body_version_mismatch", fmt.Sprintf("安装后的 %s 版本是 %q，不是记录中的 %s", b.Package, v, b.Version), "检查 npm 配置后重试")
			}
			return r.Store.Save(j)
		}
		return nil
	}
	return valise.New("body_action_invalid", "未知的身体处理方式："+b.Action, "重新生成计划")
}

func (r *Runner) npmInstall(ctx context.Context, pkg, version, registry string) error {
	args := []string{"npm", "install", "-g", "--no-fund", "--no-audit", pkg + "@" + version}
	if registry != "" {
		args = append(args, "--registry", registry)
	}
	out, err := r.Env.Run(ctx, npmTimeout, "", args...)
	if err != nil || out.Code != 0 {
		detail := ""
		if out != nil {
			detail = lastLines(string(out.Stderr), 8)
		}
		return valise.New("npm_install_failed", "按来源记录安装 "+pkg+"@"+version+" 失败", "检查网络与 npm 配置后重试").WithDetail(detail)
	}
	return nil
}

// installedNPMVersion reads the version of a globally installed package
// from <npm prefix>/lib/node_modules/<pkg>/package.json.
func installedNPMVersion(ctx context.Context, e *porterenroll.Env, pkg string) string {
	out, err := e.Run(ctx, 30*time.Second, "", "npm", "prefix", "-g")
	if err != nil || out.Code != 0 {
		return ""
	}
	prefix := strings.TrimSpace(string(out.Stdout))
	b, err := os.ReadFile(filepath.Join(prefix, "lib", "node_modules", filepath.FromSlash(pkg), "package.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		Version string `json:"version"`
	}
	json.Unmarshal(b, &meta)
	return meta.Version
}

func (r *Runner) installService(ctx context.Context, j *revive.Journal) error {
	if j.Plan.Profile.Service == nil || len(j.Plan.Profile.Service.InstallArgv) == 0 {
		return valise.New("service_install_missing", "profile 没有声明服务安装命令", "手动安装服务后重试")
	}
	argv, ok := r.Env.ExpandArgv(j.Plan.Profile.Service.InstallArgv, map[string]string{"STATE_DIR": j.Roots[0].Live})
	if !ok {
		return valise.New("service_install_env", "服务安装命令依赖未设置的环境变量", "检查 profile")
	}
	out, err := r.Env.Run(ctx, 5*time.Minute, "", argv...)
	if err != nil || out.Code != 0 {
		detail := ""
		if out != nil {
			detail = lastLines(string(out.Stdout)+string(out.Stderr), 10)
		}
		return valise.New("service_install_failed", "harness 的服务安装命令执行失败："+strings.Join(argv, " "), "按下面的输出处理后重试").WithDetail(detail)
	}
	j.ServiceInstalled = true
	return r.Store.Save(j)
}

// carry copies live content outside the snapshot's tier into the stage
// (rollback). SQLite sidecars of declared databases are never carried: they
// belong to the replaced database. Body directories are moved later.
func (r *Runner) carry(j *revive.Journal) (int, error) {
	c := classifierFor(j.Plan)
	n := 0
	for _, jr := range j.Roots {
		if !jr.LiveExisted || !exists(jr.Live) {
			continue
		}
		var bodyDirs []string
		for _, d := range j.Plan.Body.Dirs {
			if root, rel, _ := strings.Cut(d, "/"); root == jr.ID {
				bodyDirs = append(bodyDirs, filepath.Join(jr.Live, filepath.FromSlash(rel)))
			}
		}
		err := filepath.WalkDir(jr.Live, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(jr.Live, path)
			if d.IsDir() {
				for _, b := range bodyDirs {
					if path == b {
						return fs.SkipDir
					}
				}
				if rel != "." {
					if err := mkdirNoFollow(jr.Stage, filepath.Join(jr.Stage, rel)); err != nil {
						return err
					}
					if info, err := d.Info(); err == nil {
						os.Chmod(filepath.Join(jr.Stage, rel), info.Mode().Perm())
					}
				}
				return nil
			}
			rootID, rootRel := locate(j.Plan, jr.ID, filepath.ToSlash(rel))
			if _, sidecar := c.Sidecar(rootID, rootRel); sidecar {
				return nil
			}
			cat := c.Classify(rootID, rootRel).Category
			if cat == "" {
				cat = vault.CategoryUnclassified
			}
			if vault.InTier(j.Plan.Tier, cat) {
				return nil // replaced by the snapshot
			}
			dest := filepath.Join(jr.Stage, rel)
			if exists(dest) {
				return nil
			}
			if err := mkdirNoFollow(jr.Stage, filepath.Dir(dest)); err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 {
				t, err := os.Readlink(path)
				if err != nil {
					return err
				}
				return os.Symlink(t, dest)
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if err := copyFile(path, dest); err != nil {
				return err
			}
			n++
			return nil
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// locate maps a path relative to a swap root to the (nested) root it
// belongs to and its path relative to that root.
func locate(p revive.Plan, swapRoot, rel string) (string, string) {
	best, bestRel := swapRoot, rel
	depth := -1
	for _, rp := range p.Roots {
		if rp.SwapRoot != swapRoot {
			continue
		}
		if r, ok := strings.CutPrefix(rel, rp.Rel+"/"); ok && len(rp.Rel) > depth {
			best, bestRel, depth = rp.ID, r, len(rp.Rel)
		}
	}
	return best, bestRel
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, st.ModTime(), st.ModTime())
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
