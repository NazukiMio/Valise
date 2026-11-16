package service

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// ReviveOptions are the user's choices for `valise revive`.
type ReviveOptions struct {
	testOnly   bool              // internal file/stage test; never accepted from a request
	Target     string            `json:"target"`
	From       string            `json:"from"` // server whose vault holds the snapshot (default: Target)
	SnapshotID string            `json:"snapshot_id"`
	Mode       string            `json:"mode"`
	InstanceID string            `json:"instance_id,omitempty"` // clone: the new id
	Roots      map[string]string `json:"roots,omitempty"`       // --root id=path overrides
	AllowDraft bool              `json:"allow_draft_profile"`
	// AllowSnapshotProfile revives with the profile stored in the snapshot
	// when the client has no profile of that id and version.
	AllowSnapshotProfile bool `json:"allow_snapshot_profile"`
	// Source reads the snapshot from a copy instead of From's vault
	// (docs/ARCHITECTURE.md 8.2.3): repo:<名>[/<服务器名>], dir:<路径> or
	// cloud:<名>[/<服务器名>].
	Source string `json:"source,omitempty"`
	Fault  string `json:"-"` // drills only
}

// RevivePlan is shown to the user before anything changes.
type RevivePlan struct {
	// Transfer is what pulling the pod took (review F03).
	Transfer *SnapshotPull `json:"transfer,omitempty"`
	Plan     revive.Plan   `json:"plan"`
	Snapshot vault.Meta    `json:"snapshot"`
	Target   string        `json:"target"`
	From     string        `json:"from"`
	// Source is the copy the snapshot was read from, nil for From's vault.
	Source *CopySource `json:"source,omitempty"`
	// SourceLost: From could not be reached. A migration then neither stops
	// nor reads it, and leaves out exclusive secrets (8.2.3).
	SourceLost   bool   `json:"source_lost,omitempty"`
	TargetArch   string `json:"target_arch"`
	ProfileLocal bool   `json:"profile_local"`
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
	// Nested are other agents inside the target state dir: a swap is
	// refused (a restore test, which does not swap, is not).
	Nested []string `json:"nested,omitempty"`
	// UnverifiedSource is set when no snapshot binding identifies the source
	// generation: execution then needs ReviveConfirm.UnverifiedSource.
	UnverifiedSource *UnverifiedSource `json:"unverified_source,omitempty"`
	// PreRestoreBackup is the full backup taken when the person chose
	// SourceBackupFirst; set during execution.
	PreRestoreBackup *vaultrpc.SealReport `json:"pre_restore_backup,omitempty"`

	snapshotHeader *vault.Snapshot
	files          []vault.File
	links          []vault.Symlink
	sqlite         map[string]bool
	vault          *vault.Vault
	ident          age.Identity
	target         *ownerPorter
	// sourceStatus: the source porter supports service_control "status"
	// (identity/1). Older porters treat unknown actions as start.
	sourceStatus bool
	targetCaps   []string // the target porter's hello capabilities
	targetFacts  revive.InspectResult
	profileName  string
	// sourceAgent is the source generation from a bound snapshot binding.
	sourceAgent *identity.AgentRef
}

func (p *RevivePlan) block(code, reason, suggestion string) {
	p.Blockers = append(p.Blockers, *valise.New(code, reason, suggestion))
}

// PlanRevive pulls the snapshot's vault, decrypts the snapshot in memory
// and asks the target for the facts the plan needs. Nothing is changed.
func (s *Service) PlanRevive(ctx context.Context, o ReviveOptions, passphrase string) (*RevivePlan, error) {
	return s.planRevive(ctx, o, passphrase)
}

// Close releases the plan. Call it once the plan is executed or
// abandoned.
func (p *RevivePlan) Close() {}

func (s *Service) planRevive(ctx context.Context, o ReviveOptions, passphrase string) (rp *RevivePlan, err error) {
	if o.From == "" {
		o.From = o.Target
	}
	switch o.Mode {
	case revive.ModeRollback:
		if o.From != o.Target {
			return nil, valise.New("mode_mismatch", "回滚只能在快照所在的服务器上进行", "跨服务器请用 --mode migrate 或 clone")
		}
	case revive.ModeMigrate:
		if o.From == o.Target {
			return nil, valise.New("mode_mismatch", "迁移要去另一台服务器", "在同一台服务器上：恢复当前实例用 --mode rollback，另建一个副本用 --mode clone")
		}
	case revive.ModeClone:
		// A copy may stay on the snapshot's server, in directories of its
		// own that the user names (docs/UI.md 7.11); see sameServerCopy.
	default:
		return nil, valise.New("mode_invalid", "未知的复活模式："+o.Mode, "可选 rollback、migrate、clone")
	}
	var src *CopySource
	if o.Source != "" {
		if src, err = s.ParseCopySource(o.From, o.Source); err != nil {
			return nil, err
		}
	}
	st, err := s.VaultStatus(ctx, o.From)
	lost := err != nil && SourceUnreachable(err)
	switch {
	case lost && src == nil:
		return nil, s.unreachableWithCopies(ctx, o.From, o.SnapshotID, err)
	case lost && o.Mode == revive.ModeRollback:
		return nil, valise.New("source_lost", "连不上 "+o.From+"，不能在它上面恢复当前实例", "到另一台服务器用 --mode migrate 或 clone，并用 --source 指定副本")
	case err != nil && !lost:
		return nil, err
	}
	leaf := ""
	if src == nil {
		if leaf = st.LeafOf(o.SnapshotID); leaf == "" {
			return nil, valise.New("snapshot_unknown", o.From+" 上没有备份 "+o.SnapshotID, "用 valise vault status "+o.From+" 列出现有的备份")
		}
	}
	// The key comes first: which packs to fetch is in the encrypted file
	// list (review F03).
	var id age.Identity
	if passphrase == "" {
		// No passphrase given: the unlocked identity, or ask for it.
		if id, err = s.ownerIdentity(o.Target); err != nil {
			return nil, err
		}
	} else {
		k, err := s.Unlock(passphrase)
		if err != nil {
			return nil, err
		}
		defer k.Lock()
		if id, err = k.Identity(); err != nil {
			return nil, err
		}
		// The passphrase just given also proves the owner for the
		// landing (docs/ARCHITECTURE.md 2.2): no second prompt.
		s.rememberOwner(id)
	}
	var v *vault.Vault
	var pull *SnapshotPull
	if src != nil {
		var cst *vaultrpc.StatusResult
		if v, pull, cst, err = s.openFromSource(ctx, src, o.SnapshotID, id); err != nil {
			return nil, err
		}
		st, leaf = cst, cst.LeafOf(o.SnapshotID)
	} else if v, pull, err = s.PullSnapshot(ctx, o.From, leaf, o.SnapshotID, id); err != nil {
		return nil, err
	}
	head, list, err := v.Snapshot(o.SnapshotID, id)
	if err != nil {
		return nil, valise.New("snapshot_unreadable", "无法解密快照 "+o.SnapshotID, "确认快照 id 正确，且本机密钥是该快照的收件人").WithDetail(err.Error())
	}
	rp = &RevivePlan{snapshotHeader: head, Target: o.Target, From: o.From, vault: v, ident: id, sqlite: map[string]bool{}, Transfer: pull, Source: src, SourceLost: lost}
	if lost {
		rp.Warnings = append(rp.Warnings, "连不上 "+o.From+"：备份从"+src.Label()+"读取；不会停止、读取或写入 "+o.From+"，也无法确认它上面的实例已经停止")
	}
	for _, m := range v.Metas {
		if m.SnapshotID == o.SnapshotID {
			rp.Snapshot = m
		}
	}
	set, err := s.Profiles()
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
			rp.block("profile_from_snapshot", fmt.Sprintf("客户端中没有 harness 资料 %s v%d，只能使用快照里保存的副本（未经当前客户端审核）", d.ID, d.ProfileVersion),
				"检查计划中的命令后加上 --allow-snapshot-profile；或升级客户端")
		}
	} else {
		return nil, valise.New("profile_version", fmt.Sprintf("快照使用 harness 资料 %s v%d，客户端中没有同一版本，快照里也没有保存副本", head.Profile.ID, head.Profile.Version),
			"升级客户端，或用当前 harness 资料重新接入并建立备份后再恢复")
	}
	// A named instance's service and entries are not verified: never
	// stopped, started or shown (profile.ForAgent).
	prof = prof.ForAgent(head.Name)
	rp.profileName = head.Name
	if head.Name != "" && o.Mode != revive.ModeRollback && o.Roots[profile.StateRoot] == "" {
		return nil, valise.New("named_needs_root", "这是命名实例 "+head.Name+" 的备份，迁移或克隆时 Valise 不猜目标目录（默认目录属于另一个 agent）",
			"用 --root state=<路径> 指定目标目录")
	}
	// Landing is judged by the restore capability alone (docs/PROFILES.md
	// 1.2), not by the profile's overall draft status.
	if st := prof.CapabilityStatus("restore"); st != "tested" && !o.AllowDraft {
		rp.block("profile_draft", "harness 资料 "+prof.ID+" v"+strconv.Itoa(prof.ProfileVersion)+" 的恢复尚未在此版本测试（"+st+"），复活需要显式同意",
			"确认风险后加上 --allow-draft-profile")
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
	// A copy beside the original: Valise places no directory for it and
	// leaves the service alone, since the original runs under that unit.
	beside := o.Mode == revive.ModeClone && o.From == o.Target
	if beside {
		switch {
		case instance == head.InstanceID:
			return nil, valise.New("copy_same_id", "副本不能和原实例用同一个标识 "+instance, "给副本起另一个标识（--id）")
		case o.Roots[profile.StateRoot] == "":
			return nil, valise.New("copy_needs_root", "副本和原实例在同一台服务器上，Valise 不替你猜它的目录（默认目录属于原实例）",
				"给副本指定不与原实例重合的目录：--root state=<路径>")
		}
		prof.Service, prof.Entries = nil, nil
	}

	// Target facts, first to resolve the state dir on the target. Landing
	// is an owner operation: the daily key plus the owner proof.
	report(ctx, StageInspect, 0, 0)
	if rp.target, err = s.ownerPorter(ctx, o.Target); err != nil {
		return nil, err
	}
	var first revive.InspectResult
	if err := s.callOwner(ctx, rp.target, nil, revive.MethodInspect, revive.InspectParams{Profile: prof, InstanceID: instance}, &first); err != nil {
		return nil, err
	}
	roots, err := mapRoots(o, head, first, prof)
	if err != nil {
		return nil, err
	}
	var facts revive.InspectResult
	if err := s.callOwner(ctx, rp.target, nil, revive.MethodInspect,
		revive.InspectParams{Profile: prof, InstanceID: instance, Roots: roots, Provenance: head.Provenance, PlainBytes: rp.Snapshot.PlainBytes}, &facts); err != nil {
		return nil, err
	}
	rp.TargetArch = facts.OS + "/" + facts.Arch
	rp.targetFacts = facts
	plan := revive.Plan{OpID: revive.NewOpID(time.Now()), Mode: o.Mode, InstanceID: instance, SnapshotID: o.SnapshotID,
		Tier: head.Tier, Profile: prof, Roots: roots, Carry: o.Mode == revive.ModeRollback}
	if len(facts.Busy) > 0 && !o.testOnly {
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
	if len(facts.Nested) > 0 && !facts.NestedShared {
		rp.Nested = facts.Nested
	}
	if o.Mode == revive.ModeRollback {
		if facts.Record == nil {
			rp.block("not_enrolled", "目标服务器上没有接入 Valise 的实例 "+instance, "恢复当前实例针对已接入的实例；跨服务器请用 migrate")
		} else if sup := facts.Record.Superseded; sup != nil {
			rp.block("superseded", "实例 "+instance+" 的目录已被实例 "+sup.By+" 取代（操作 "+sup.OpID+"）",
				"回滚 "+sup.By+"；不再需要旧实例时用 valise old cleanup 清理")
		} else {
			plan.Overrides = facts.Record.LocalOverrides
		}
	} else if len(facts.Inside) > 0 {
		rp.block("root_inside_other", "目标目录位于已接入的实例 "+strings.Join(facts.Inside, "、")+" 的目录之内，不能把一个实例放进另一个实例的目录里",
			"换一个不在其他实例目录里的目标目录（--root）；如果是这个 harness 的命名实例，放到它声明的命名实例位置")
	}
	if beside {
		sameServerCopy(rp, instance, facts, prof)
	} else if o.Mode != revive.ModeRollback && len(facts.Overlapping) > 0 {
		plan.Supersedes = facts.Overlapping
		rp.Warnings = append(rp.Warnings, "目标上已接入的实例 "+strings.Join(facts.Overlapping, "、")+
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
	if o.Mode == revive.ModeClone || (o.Mode == revive.ModeMigrate && lost) {
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

	if !o.testOnly {
		planBody(rp, &plan, head, facts, prof, o.Mode)
		if beside {
			how := ""
			if prof.Relocation != nil {
				how = "；要运行副本，启动时把 " + prof.Relocation.Env + " 指向它的目录"
			}
			rp.Warnings = append(rp.Warnings, "副本和原实例在同一台服务器上：Valise 只把文件放进你指定的目录，不启动、不停止任何服务；副本要用的服务、端口与登录由你另行配置"+how)
		} else {
			planService(rp, &plan, facts, prof)
		}
	} else {
		plan.TestOnly = true
		plan.Carry = false
		plan.Body.Action = revive.BodyKeep
	}
	// Named instances of this profile nested inside the target run under
	// the same service: stopped with it, their directories are carried
	// into the new live directory (docs/PROFILES.md 1.3).
	if facts.NestedShared {
		if plan.Unit == "" {
			rp.Nested = facts.Nested
		} else {
			for _, rel := range facts.NestedRel {
				plan.Keep = append(plan.Keep, profile.StateRoot+"/"+rel)
			}
			rp.Warnings = append(rp.Warnings, fmt.Sprintf("目录里还有 %d 个共用服务 %s 的 agent（%s）：交换时它们会一起短暂停下，目录原样搬进新状态，不会被替换", len(facts.NestedRel), plan.Unit, strings.Join(facts.NestedRel, "、")))
		}
	}
	if o.Mode == revive.ModeMigrate && lost {
		excluded := "这个备份里没有 harness 资料声明的独占凭据"
		if len(rp.Excluded) > 0 {
			excluded = "独占凭据不会带过去（见下面排除的文件），在新服务器上需要重新登录"
		}
		rp.Warnings = append(rp.Warnings, "失联迁移："+excluded+"；"+o.From+" 以后又连上时，Valise 会提醒两边可能都在运行")
	} else if o.Mode == revive.ModeMigrate && plan.Unit != "" {
		rp.StopSource = plan.Unit
		rp.Warnings = append(rp.Warnings, "目标启动前会停止原服务器上的 "+plan.Unit+"（原实例的文件不动）；复活失败时会重新启动它")
	}
	if o.Mode != revive.ModeRollback {
		rootsMap := map[string]string{}
		for _, r := range roots {
			rootsMap[r.ID] = r.Path
		}
		rec := enroll.Record{InstanceID: instance, ProfileID: prof.ID, ProfileVersion: prof.ProfileVersion, Roots: rootsMap,
			Provenance: head.Provenance, LocalOverrides: append([]profile.Override{}, head.LocalOverrides...), EnrolledAt: time.Now().UTC(),
			Name: head.Name}
		if len(head.Env) > 0 {
			rec.Env = map[string]string{}
			for k, v := range head.Env { // a path-based selection follows the new state dir
				rec.Env[k] = strings.ReplaceAll(v, head.Roots[profile.StateRoot], rootsMap[profile.StateRoot])
			}
		}
		plan.Record = &rec
	}

	if !o.testOnly {
		if err := s.prepareRecoveryIdentity(ctx, rp, &plan); err != nil {
			return nil, err
		}
		planNestedBody(rp, &plan)
		warnCredentialsReplaced(rp, &plan, facts, prof)
		describeUnverifiedSource(rp, head, periodOf(st, leaf), facts, instance)
	}
	rp.NeedBytes = rp.Bytes
	if plan.Carry {
		rp.Warnings = append(rp.Warnings, "回滚会把档位以外的现有内容（凭据、缓存、未分类文件等）复制进新状态，另需相应空间；身体目录只改名搬动，不复制")
	}
	if uint64(rp.NeedBytes) > rp.FreeBytes {
		rp.block("space_low", fmt.Sprintf("目标服务器空间不足：需要约 %d MiB，可用 %d MiB", rp.NeedBytes>>20, rp.FreeBytes>>20), "释放空间后重试")
	}
	if o.testOnly {
		rp.Plan = plan
		rp.Downtime = "本次只测试文件与暂存，不估算实际恢复的停机时间"
		return rp, nil
	}
	grace := 20
	if prof.Health != nil && prof.Health.GraceSeconds > 0 {
		grace = prof.Health.GraceSeconds
	}
	rp.Downtime = fmt.Sprintf("从停止服务到健康检查通过：至少约 %d 秒（宽限 %d 秒 + 3 次检查），最长约 %d 秒", grace+15, grace, grace+120)
	if plan.Unit == "" {
		rp.Downtime = "没有 Valise 控制的服务：只交换文件，几秒钟；agent 要你自己重新启动"
	}
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

// sameServerCopy refuses a copy on the snapshot's own server that would
// touch another instance there, or that its harness could not use: its
// directories may not be, hold or sit inside an enrolled instance's, and
// its id may not be taken. Elsewhere a copy over an instance supersedes it;
// beside the original it never does.
func sameServerCopy(rp *RevivePlan, instance string, facts revive.InspectResult, prof profile.Profile) {
	// A harness whose state directory cannot be moved would never use a
	// copy placed elsewhere under the same account (docs/PROFILES.md).
	if prof.Relocation == nil {
		rp.block("copy_not_relocatable", "harness 资料 "+prof.ID+" 没有声明状态目录可以改到别处，同一台服务器上的副本无法被它使用",
			"把副本建到另一台服务器；或等这个 harness 的资料声明 relocation")
	}
	if facts.Record != nil {
		rp.block("copy_id_taken", "这台服务器上已经有实例 "+instance, "给副本起另一个标识（--id）")
	}
	if len(facts.Overlapping) > 0 {
		rp.block("copy_overlaps", "副本的目录和已接入的实例 "+strings.Join(facts.Overlapping, "、")+" 的目录重合",
			"给副本的每个目录另指定位置（--root <名称>=<路径>），不与原实例共用")
	}
	if len(facts.Nested) > 0 {
		rp.block("copy_contains_other", "副本的目录里包含已接入实例的目录："+strings.Join(facts.Nested, "、"), "换一个里面没有其他实例的目录（--root）")
	}
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
	for id, p := range paths {
		paths[id] = filepath.Clean(p)
	}
	roots, err := revive.CanonicalRoots(paths)
	if err != nil {
		return nil, valise.New("root_layout_invalid", "备份中的内容目录位置重复或不明确，无法准备恢复计划", "检查 Agent 接入时记录的保存位置，核实或重新接入后再准备恢复")
	}
	return roots, nil
}

// planBody decides how the body is handled.
func planBody(rp *RevivePlan, plan *revive.Plan, head *vault.Snapshot, facts revive.InspectResult, prof profile.Profile, mode string) {
	pv := head.Provenance
	var dirs []string
	if prof.Body != nil {
		for _, b := range prof.Body.Paths {
			d, ok := strings.CutSuffix(b.Glob, "/**")
			if !ok || !profile.IsLiteral(d) {
				rp.block("body_glob", "harness 资料的身体路径 "+b.Glob+" 不是 <目录>/** 形式，v0.1 无法在交换中搬动", "修正 harness 资料")
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
	// A kept or reinstalled body inside a replaced root travels with the
	// swap (docs/ARCHITECTURE.md 8.2.1); only roots the target has.
	var nested []string
	for _, d := range dirs {
		root, _, _ := strings.Cut(d, "/")
		for _, f := range facts.Roots {
			if f.ID == root && f.Exists {
				nested = append(nested, d)
			}
		}
	}
	switch {
	case pv.NPM != nil && pv.NPM.Version == "":
		// Nothing was recorded at enrollment: say so, not "changed".
		rp.block("provenance_missing", "接入时没有记下 "+pv.NPM.Package+" 的来源版本，无法核对目标上的身体",
			"在原服务器上重新接入（让 Valise 记下来源），再建立备份；或按官方文档在目标上全新安装："+freshDoc(prof))
		return
	case pv.NPM != nil:
		installed := facts.Provenance.NPM != nil && facts.Provenance.NPM.Version == pv.NPM.Version
		if installed {
			plan.Body = revive.BodyPlan{Action: revive.BodyKeep, Dirs: nested}
			return
		}
		if !facts.Tools["npm"] {
			rp.block("runtime_missing", "目标服务器的登录 PATH 中没有 npm，无法按来源安装 "+pv.NPM.Package,
				"先在目标服务器上安装 Node.js 与 npm（用户级即可），参考 "+freshDoc(prof))
			return
		}
		if pv.NPM.Integrity == "" {
			rp.block("provenance_missing", "接入时没有记下 "+pv.NPM.Package+"@"+pv.NPM.Version+" 的 integrity，无法核对 registry 上的版本",
				"在原服务器上重新接入，再建立备份；或按官方文档全新安装："+freshDoc(prof))
			return
		}
		if facts.RegistryCheck != pv.NPM.Integrity {
			rp.block("provenance_changed", fmt.Sprintf("registry 上 %s@%s 的 integrity 与接入时记录的不一致（或无法查询）", pv.NPM.Package, pv.NPM.Version),
				"来源已变化，Valise 不会自动安装。核实后可按官方文档全新安装："+freshDoc(prof))
			return
		}
		plan.Body = revive.BodyPlan{Action: revive.BodyNPMInstall, Package: pv.NPM.Package, Version: pv.NPM.Version, Registry: pv.NPM.Registry, Integrity: pv.NPM.Integrity, Dirs: nested}
	case pv.Git != nil:
		same := facts.Provenance.Git != nil && facts.Provenance.Git.Commit == pv.Git.Commit && facts.Provenance.Git.Remote == pv.Git.Remote
		switch {
		case head.Tier == vault.TierRaw:
			rp.Warnings = append(rp.Warnings, "身体取自 raw 快照，只在同架构、同路径时可用")
			plan.Body = revive.BodyPlan{Action: revive.BodyMoveDirs, Dirs: dirs}
		case same && mode == revive.ModeRollback:
			plan.Body = revive.BodyPlan{Action: revive.BodyMoveDirs, Dirs: dirs}
		default:
			rp.block("body_rebuild_unverified", "目标服务器上没有同一 commit 的身体，而 harness 资料的重建步骤（bootstrap_argv）尚未核实",
				"在目标服务器上按官方文档安装同一版本后重试（"+freshDoc(prof)+"），或使用同架构、同路径的 raw 快照")
		}
	default:
		plan.Body = revive.BodyPlan{Action: revive.BodyKeep, Dirs: nested}
	}
}

// planNestedBody checks that the target porter carries a kept body inside a
// replaced root. An older porter still rolls back (the carry copies the
// body), but cannot migrate or clone over it.
func planNestedBody(rp *RevivePlan, plan *revive.Plan) {
	b := &plan.Body
	if len(b.Dirs) == 0 || b.Action == revive.BodyMoveDirs || slices.Contains(rp.targetCaps, revive.BodyDirsCapability) {
		return
	}
	if plan.Mode == revive.ModeRollback {
		b.Dirs = nil
		return
	}
	rp.block("unsupported_capability", rp.Target+" 上的搬运工还不会把装在状态目录里的身体（"+strings.Join(b.Dirs, "、")+"）搬进新目录，交换后服务会起不来",
		"先更新 "+rp.Target+" 上的搬运工（valise porter deploy），再重新准备恢复")
}

// warnCredentialsReplaced: a backup without credentials replacing a
// directory the target already has leaves the harness without its
// configuration (docs/ARCHITECTURE.md 8.2.1).
func warnCredentialsReplaced(rp *RevivePlan, plan *revive.Plan, facts revive.InspectResult, prof profile.Profile) {
	if plan.Mode == revive.ModeRollback || rp.RestoreSecret {
		return
	}
	declared := false
	for _, r := range prof.Paths {
		declared = declared || r.Category == "secrets"
	}
	for _, f := range facts.Roots {
		if declared && f.ID == profile.StateRoot && f.Exists {
			rp.Warnings = append(rp.Warnings, "这个备份不含凭据：目标上 "+f.Path+" 原有的配置与凭据会随旧目录一起换下（保留在交换前的目录里），"+
				prof.DisplayName+" 可能因缺少配置起不来并自动退回；可改用含凭据的备份，或恢复后在原生界面重新登录")
			return
		}
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
			rp.block("service_missing", "目标服务器上没有服务 "+plan.Unit+"，harness 资料也没有声明安装命令", "手动安装该服务后重试")
			return
		}
		plan.InstallService = true
		rp.InstallArgv = prof.Service.InstallArgv
	}
}
