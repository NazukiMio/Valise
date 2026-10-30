package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"

	"valise/client/porter"
	"valise/client/profiles"
	"valise/client/ssh"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
)

// ReviveOptions are the user's choices for `valise revive`.
type ReviveOptions struct {
	Target     string            `json:"target"`
	From       string            `json:"from"` // server whose vault holds the snapshot (default: Target)
	SnapshotID string            `json:"snapshot_id"`
	Mode       string            `json:"mode"`
	InstanceID string            `json:"instance_id,omitempty"` // clone: the new id
	Roots      map[string]string `json:"roots,omitempty"`       // --root id=path overrides
	AllowDraft bool              `json:"allow_draft_profile"`
	// AllowSnapshotProfile revives with the profile stored in the snapshot
	// when the client has no profile of that id and version.
	AllowSnapshotProfile bool   `json:"allow_snapshot_profile"`
	Fault                string `json:"-"` // drills only
}

// RevivePlan is shown to the user before anything changes.
type RevivePlan struct {
	Plan         revive.Plan `json:"plan"`
	Snapshot     vault.Meta  `json:"snapshot"`
	Target       string      `json:"target"`
	From         string      `json:"from"`
	TargetArch   string      `json:"target_arch"`
	ProfileLocal bool        `json:"profile_local"`
	// ProfileFromSnapshot: the profile came from the snapshot header, not
	// reviewed by this client.
	ProfileFromSnapshot bool               `json:"profile_from_snapshot"`
	Files               int                `json:"files"`
	Bytes               int64              `json:"bytes"`
	Excluded            []string           `json:"excluded_exclusive_secrets,omitempty"`
	RestoreSecret       bool               `json:"restores_secrets"`
	StopSource          string             `json:"stop_source_unit,omitempty"` // migrate: unit stopped on From
	InstallArgv         []string           `json:"service_install_argv,omitempty"`
	Downtime            string             `json:"downtime"`
	NeedBytes           int64              `json:"need_bytes"`
	FreeBytes           uint64             `json:"free_bytes"`
	Warnings            []string           `json:"warnings,omitempty"`
	Blockers            []valise.UserError `json:"blockers,omitempty"`

	files   []vault.File
	links   []vault.Symlink
	sqlite  map[string]bool
	vault   *vault.Vault
	ident   age.Identity
	source  *ssh.Transport
	target  *ssh.Transport
	porterP string
}

func (p *RevivePlan) block(code, reason, suggestion string) {
	p.Blockers = append(p.Blockers, *valise.New(code, reason, suggestion))
}

// PlanRevive pulls the snapshot's vault, decrypts the snapshot in memory
// and asks the target for the facts the plan needs. Nothing is changed.
func (s *Service) PlanRevive(ctx context.Context, o ReviveOptions, passphrase string) (*RevivePlan, error) {
	if o.From == "" {
		o.From = o.Target
	}
	switch o.Mode {
	case revive.ModeRollback:
		if o.From != o.Target {
			return nil, valise.New("mode_mismatch", "回滚只能在快照所在的服务器上进行", "跨服务器请用 --mode migrate 或 clone")
		}
	case revive.ModeMigrate, revive.ModeClone:
		if o.From == o.Target {
			return nil, valise.New("mode_mismatch", "v0.1 的迁移与克隆只支持到另一台服务器", "在同一台服务器上请用 --mode rollback")
		}
	default:
		return nil, valise.New("mode_invalid", "未知的复活模式："+o.Mode, "可选 rollback、migrate、clone")
	}
	pull, err := s.Pull(ctx, o.From, "")
	if err != nil {
		return nil, err
	}
	if len(pull.Problems) > 0 {
		return nil, valise.New("vault_problems", "本机金库副本没有通过校验", "先运行 valise verify --vault "+pull.Dir)
	}
	k, err := s.Unlock(passphrase)
	if err != nil {
		return nil, err
	}
	defer k.Lock()
	id, err := k.Identity()
	if err != nil {
		return nil, err
	}
	v, err := vault.Open(pull.Dir)
	if err != nil {
		return nil, err
	}
	head, list, err := v.Snapshot(o.SnapshotID, id)
	if err != nil {
		return nil, valise.New("snapshot_unreadable", "无法解密快照 "+o.SnapshotID, "确认快照 id 正确，且本机密钥是该快照的收件人").WithDetail(err.Error())
	}
	rp := &RevivePlan{Target: o.Target, From: o.From, vault: v, ident: id, sqlite: map[string]bool{}}
	for _, m := range v.Metas {
		if m.SnapshotID == o.SnapshotID {
			rp.Snapshot = m
		}
	}
	set, err := profiles.Load(profiles.LocalDir(s.Dirs.Data))
	if err != nil {
		return nil, err
	}
	var prof profile.Profile
	if lp := set.ByID[head.Profile.ID]; lp != nil && lp.Effective.ProfileVersion == head.Profile.Version {
		prof = *lp.Effective
		rp.ProfileLocal = lp.Local
	} else if d := head.ProfileDoc; d != nil && d.ID == head.Profile.ID && d.ProfileVersion == head.Profile.Version {
		prof = *d
		rp.ProfileFromSnapshot = true
		if !o.AllowSnapshotProfile {
			rp.block("profile_from_snapshot", fmt.Sprintf("客户端中没有 profile %s v%d，只能使用快照里保存的副本（未经当前客户端审核）", d.ID, d.ProfileVersion),
				"检查计划中的命令后加上 --allow-snapshot-profile；或升级客户端")
		}
	} else {
		return nil, valise.New("profile_version", fmt.Sprintf("快照使用 profile %s v%d，客户端中没有同一版本，快照里也没有保存副本", head.Profile.ID, head.Profile.Version),
			"升级客户端，或用当前 profile 重新登记并封存后再复活")
	}
	if prof.Status != profile.StatusVerified && !o.AllowDraft {
		rp.block("profile_draft", "profile "+prof.ID+" 仍是 draft（未核实），复活需要显式同意", "确认风险后加上 --allow-draft-profile")
	}

	instance := head.InstanceID
	if o.Mode == revive.ModeClone {
		instance = o.InstanceID
		if instance == "" {
			instance = head.InstanceID + "-clone"
		}
	}
	if enroll.ValidInstanceID(instance) != nil {
		return nil, valise.New("instance_id_invalid", "实例 id 无效："+instance, "用 --id 指定")
	}

	// Target facts, first to resolve the state dir on the target.
	if rp.target, _, err = s.transport(o.Target); err != nil {
		return nil, err
	}
	found, err := porter.Probe(ctx, rp.target)
	if err != nil {
		return nil, err
	}
	rp.porterP = found.Path
	if rp.source, _, err = s.transport(o.From); err != nil {
		return nil, err
	}
	var first revive.InspectResult
	if err := s.callOn(ctx, rp.target, rp.porterP, nil, revive.MethodInspect, revive.InspectParams{Profile: prof, InstanceID: instance}, &first); err != nil {
		return nil, err
	}
	roots, err := mapRoots(o, head, first, prof)
	if err != nil {
		return nil, err
	}
	var facts revive.InspectResult
	if err := s.callOn(ctx, rp.target, rp.porterP, nil, revive.MethodInspect,
		revive.InspectParams{Profile: prof, InstanceID: instance, Roots: roots, Provenance: head.Provenance, PlainBytes: rp.Snapshot.PlainBytes}, &facts); err != nil {
		return nil, err
	}
	rp.TargetArch = facts.OS + "/" + facts.Arch
	plan := revive.Plan{OpID: revive.NewOpID(time.Now()), Mode: o.Mode, InstanceID: instance, SnapshotID: o.SnapshotID,
		Tier: head.Tier, Profile: prof, Roots: roots, Carry: o.Mode == revive.ModeRollback}
	if len(facts.Busy) > 0 {
		var desc []string
		for _, b := range facts.Busy {
			desc = append(desc, fmt.Sprintf("pid %d（%s）", b.PID, b.Command))
		}
		rp.block("root_busy", "除了服务 "+firstUnit(prof)+" 之外，还有进程在使用状态目录："+strings.Join(desc, "；"),
			"先停止这些进程再复活（Valise 不会替你结束它们）；否则交换期间它们可能重新创建目录")
	}
	if len(facts.PendingOps) > 0 {
		rp.block("op_pending", "目标服务器上有未完成的操作："+strings.Join(facts.PendingOps, "、"), "先用 valise revive resume 处理")
	}
	if o.Mode == revive.ModeRollback {
		if facts.Record == nil {
			rp.block("not_enrolled", "目标服务器上没有登记实例 "+instance, "回滚针对已登记的实例；跨服务器请用 migrate")
		} else if sup := facts.Record.Superseded; sup != nil {
			rp.block("superseded", "实例 "+instance+" 的目录已被实例 "+sup.By+" 取代（操作 "+sup.OpID+"）",
				"回滚 "+sup.By+"；不再需要旧实例时用 valise old cleanup 清理")
		} else {
			plan.Overrides = facts.Record.LocalOverrides
		}
	} else if len(facts.Overlapping) > 0 {
		plan.Supersedes = facts.Overlapping
		rp.Warnings = append(rp.Warnings, "目标上已登记的实例 "+strings.Join(facts.Overlapping, "、")+
			" 使用同一目录：交换成功后它的旧目录保留为 prev，记录标记为\"已被取代\"，不再能封存；用 valise old list 查看，valise old cleanup 清理")
	}
	for _, f := range facts.Roots {
		if f.Symlinked {
			rp.block("root_symlinked", "目标目录 "+f.Path+" 本身是符号链接，v0.1 不支持在链接背后交换", "改用真实目录")
		}
		if !f.SameFS {
			rp.block("not_same_fs", f.Path+" 与父目录不在同一文件系统", "暂存目录必须与活动目录在同一文件系统")
		}
		rp.FreeBytes = f.FreeBytes
	}

	// Files to stage.
	var excl []profile.PathRef
	if o.Mode == revive.ModeClone {
		excl = prof.ExclusiveSecrets
	}
	for _, f := range list.Files {
		if f.Root == vault.BodyPatchRoot {
			continue // applied only when rebuilding a git body
		}
		if matchAny(excl, f.Root, f.Path) {
			rp.Excluded = append(rp.Excluded, f.Root+":"+f.Path)
			continue
		}
		if f.Category == "secrets" {
			rp.RestoreSecret = true
		}
		rp.files = append(rp.files, f)
		rp.Files++
		rp.Bytes += f.Size
	}
	rp.links = list.Symlinks
	for _, sq := range head.SQLite {
		rp.sqlite[sq.Root+":"+sq.Path] = true
	}

	planBody(rp, &plan, head, facts, prof, o.Mode)
	planService(rp, &plan, facts, prof)
	if o.Mode == revive.ModeMigrate && plan.Unit != "" {
		rp.StopSource = plan.Unit
		rp.Warnings = append(rp.Warnings, "目标启动前会停止原服务器上的 "+plan.Unit+"（原实例的文件不动）；复活失败时会重新启动它")
	}
	if o.Mode != revive.ModeRollback {
		rootsMap := map[string]string{}
		for _, r := range roots {
			rootsMap[r.ID] = r.Path
		}
		rec := enroll.Record{InstanceID: instance, ProfileID: prof.ID, ProfileVersion: prof.ProfileVersion, Roots: rootsMap,
			Provenance: head.Provenance, LocalOverrides: append([]profile.Override{}, head.LocalOverrides...), EnrolledAt: time.Now().UTC()}
		plan.Record = &rec
	}

	rp.NeedBytes = rp.Bytes
	if plan.Carry {
		rp.Warnings = append(rp.Warnings, "回滚会把档位以外的现有内容（凭据、缓存、未分类文件等）复制进新状态，另需相应空间；身体目录只改名搬动，不复制")
	}
	if uint64(rp.NeedBytes) > rp.FreeBytes {
		rp.block("space_low", fmt.Sprintf("目标服务器空间不足：需要约 %d MiB，可用 %d MiB", rp.NeedBytes>>20, rp.FreeBytes>>20), "释放空间后重试")
	}
	grace := 20
	if prof.Health != nil && prof.Health.GraceSeconds > 0 {
		grace = prof.Health.GraceSeconds
	}
	rp.Downtime = fmt.Sprintf("从停止服务到健康检查通过：至少约 %d 秒（宽限 %d 秒 + 3 次检查），最长约 %d 秒", grace+15, grace, grace+120)
	if plan.Body.Action == revive.BodyNPMInstall {
		rp.Downtime += "；另加按来源安装身体的时间"
	}
	if head.Tier == vault.TierRaw {
		rp.Warnings = append(rp.Warnings, "这是 raw 快照：会带回过期的 pid、锁和心跳文件；服务启动后由健康检查确认是否正常")
	} else if rp.Files > 0 {
		rp.Warnings = append(rp.Warnings, "未分类文件（如锁文件）会随凭据层一起恢复；复活前会先停止服务")
	}
	rp.Plan = plan
	return rp, nil
}

func matchAny(refs []profile.PathRef, root, path string) bool {
	for _, r := range refs {
		if r.Root == root && profile.MatchGlob(r.Glob, path) {
			return true
		}
	}
	return false
}

// mapRoots places the snapshot's roots on the target.
func mapRoots(o ReviveOptions, head *vault.Snapshot, first revive.InspectResult, prof profile.Profile) ([]revive.RootPlan, error) {
	oldState := head.Roots[profile.StateRoot]
	newState := oldState
	if o.Mode != revive.ModeRollback {
		newState = first.StateDir
	}
	if p, ok := o.Roots[profile.StateRoot]; ok {
		newState = p
	}
	if newState == "" {
		return nil, valise.New("state_dir_unknown", "无法确定目标服务器上的状态目录", "用 --root state=<路径> 指定")
	}
	oldHome := ""
	if prof.Detect != nil {
		for _, c := range prof.Detect.StateDirCandidates {
			if rest, ok := strings.CutPrefix(c, "${HOME}"); ok && strings.HasSuffix(oldState, rest) {
				oldHome = strings.TrimSuffix(oldState, rest)
			}
		}
	}
	paths := map[string]string{profile.StateRoot: newState}
	for id, old := range head.Roots {
		if id == profile.StateRoot {
			continue
		}
		switch {
		case o.Roots[id] != "":
			paths[id] = o.Roots[id]
		case o.Mode == revive.ModeRollback:
			paths[id] = old
		case strings.HasPrefix(old, oldState+"/"):
			paths[id] = newState + strings.TrimPrefix(old, oldState)
		case oldHome != "" && strings.HasPrefix(old, oldHome+"/"):
			paths[id] = first.Home + strings.TrimPrefix(old, oldHome)
		default:
			return nil, valise.New("root_unmapped", "无法确定 root "+id+"（原路径 "+old+"）在目标服务器上的位置", "用 --root "+id+"=<路径> 指定")
		}
	}
	var out []revive.RootPlan
	for id, p := range paths {
		rp := revive.RootPlan{ID: id, Path: filepath.Clean(p)}
		for oid, op := range paths {
			if oid != id && strings.HasPrefix(rp.Path, filepath.Clean(op)+"/") {
				rp.SwapRoot, rp.Rel = oid, strings.TrimPrefix(rp.Path, filepath.Clean(op)+"/")
			}
		}
		out = append(out, rp)
	}
	return out, nil
}

// planBody decides how the body is handled.
func planBody(rp *RevivePlan, plan *revive.Plan, head *vault.Snapshot, facts revive.InspectResult, prof profile.Profile, mode string) {
	pv := head.Provenance
	var dirs []string
	if prof.Body != nil {
		for _, b := range prof.Body.Paths {
			d, ok := strings.CutSuffix(b.Glob, "/**")
			if !ok || !profile.IsLiteral(d) {
				rp.block("body_glob", "profile 的身体路径 "+b.Glob+" 不是 <目录>/** 形式，v0.1 无法在交换中搬动", "修正 profile")
				continue
			}
			swap := b.Root
			for _, r := range plan.Roots {
				if r.ID == b.Root && r.SwapRoot != "" {
					swap, d = r.SwapRoot, r.Rel+"/"+d
				}
			}
			dirs = append(dirs, swap+"/"+d)
		}
	}
	switch {
	case pv.NPM != nil:
		installed := facts.Provenance.NPM != nil && facts.Provenance.NPM.Version == pv.NPM.Version
		if installed {
			plan.Body = revive.BodyPlan{Action: revive.BodyKeep}
			return
		}
		if !facts.Tools["npm"] {
			rp.block("runtime_missing", "目标服务器的登录 PATH 中没有 npm，无法按来源安装 "+pv.NPM.Package,
				"先在目标服务器上安装 Node.js 与 npm（用户级即可），参考 "+freshDoc(prof))
			return
		}
		if pv.NPM.Integrity == "" || facts.RegistryCheck != pv.NPM.Integrity {
			rp.block("provenance_changed", fmt.Sprintf("registry 上 %s@%s 的 integrity 与登记时记录的不一致（或无法查询）", pv.NPM.Package, pv.NPM.Version),
				"来源已变化，Valise 不会自动安装。核实后可按官方文档全新安装："+freshDoc(prof))
			return
		}
		plan.Body = revive.BodyPlan{Action: revive.BodyNPMInstall, Package: pv.NPM.Package, Version: pv.NPM.Version, Registry: pv.NPM.Registry, Integrity: pv.NPM.Integrity}
	case pv.Git != nil:
		same := facts.Provenance.Git != nil && facts.Provenance.Git.Commit == pv.Git.Commit && facts.Provenance.Git.Remote == pv.Git.Remote
		switch {
		case head.Tier == vault.TierRaw:
			rp.Warnings = append(rp.Warnings, "身体取自 raw 快照，只在同架构、同路径时可用")
			plan.Body = revive.BodyPlan{Action: revive.BodyMoveDirs, Dirs: dirs}
		case same && mode == revive.ModeRollback:
			plan.Body = revive.BodyPlan{Action: revive.BodyMoveDirs, Dirs: dirs}
		default:
			rp.block("body_rebuild_unverified", "目标服务器上没有同一 commit 的身体，而 profile 的重建步骤（bootstrap_argv）尚未核实",
				"在目标服务器上按官方文档安装同一版本后重试（"+freshDoc(prof)+"），或使用同架构、同路径的 raw 快照")
		}
	default:
		plan.Body = revive.BodyPlan{Action: revive.BodyKeep}
	}
}

func firstUnit(p profile.Profile) string {
	if p.Service != nil && len(p.Service.Candidates) > 0 {
		return p.Service.Candidates[0].Unit
	}
	return "（无）"
}

func freshDoc(p profile.Profile) string {
	if p.Body != nil && p.Body.FreshInstallDoc != "" {
		return p.Body.FreshInstallDoc
	}
	return p.Docs
}

// planService picks the unit and whether it must be installed.
func planService(rp *RevivePlan, plan *revive.Plan, facts revive.InspectResult, prof profile.Profile) {
	if prof.Service == nil || len(prof.Service.Candidates) == 0 {
		rp.Warnings = append(rp.Warnings, "这个 harness 没有服务单元（前台运行）：Valise 只恢复文件，不会启动它")
		return
	}
	plan.Unit = prof.Service.Candidates[0].Unit
	loaded := false
	for _, st := range facts.Service {
		if st.Unit == plan.Unit && st.LoadState == "loaded" {
			loaded = true
		}
	}
	if !loaded {
		if len(prof.Service.InstallArgv) == 0 {
			rp.block("service_missing", "目标服务器上没有服务 "+plan.Unit+"，profile 也没有声明安装命令", "手动安装该服务后重试")
			return
		}
		plan.InstallService = true
		rp.InstallArgv = prof.Service.InstallArgv
	}
}
