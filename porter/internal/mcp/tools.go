package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
	cp "valise/core/protection"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/porter/internal/checkup"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/kit"
	porterprotection "valise/porter/internal/protection"
	"valise/porter/internal/seal"
)

var errUnknownTool = errors.New("unknown tool")

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var toolList = []map[string]any{
	{"name": "valise_status", "description": "Fast read-only backup status: active Protection Plan summary, last backup, verbatim protection_summary, and pending_proposals count. Never scans files or writes backups.",
		"inputSchema": schema(map[string]any{})},
	{"name": "valise_seal", "description": "Create a rolling kind:auto backup right before high-risk self-edits (core config, plugins/skills, major persona/rule/memory rewrites, bulk workspace edits, or harness upgrade) or on user request. Uses the instance's Protection Plan tier (full by default), or conservative soul tier with plan_warning. Rate-limited to once per 10 minutes.",
		"inputSchema": schema(map[string]any{"reason": map[string]any{"type": "string", "description": "Optional short reason for the audit log (no secrets)"}})},
	{"name": "valise_onboard_context", "description": "Read-only onboarding context for valise-onboard: instance roots, has_secret_rules, Protection Plan, effective dossier rules, layout_facts (unclassified_dirs, largest_dirs, external_symlinks, churn_files, untracked_sqlite, known_entries, loopback_ports), and pending_proposals. Never triggers a backup.",
		"inputSchema": schema(map[string]any{})},
	{"name": "valise_propose_dossier", "description": "Queue validated dossier proposals (classify, sqlite, external_ref, tool_dep, web_entry) for user approval in Valise desktop. Never modifies the dossier directly.",
		"inputSchema": schema(map[string]any{"items": map[string]any{"type": "array", "items": schema(map[string]any{
			"kind":               map[string]any{"type": "string", "enum": []string{"classify", "sqlite", "external_ref", "tool_dep", "web_entry"}},
			"id":                 map[string]any{"type": "string"},
			"display_name":       map[string]any{"type": "string"},
			"port":               map[string]any{"type": "integer"},
			"hint":               map[string]any{"type": "string"},
			"root":               map[string]any{"type": "string"},
			"glob":               map[string]any{"type": "string"},
			"path":               map[string]any{"type": "string"},
			"category":           map[string]any{"type": "string", "enum": []string{"identity", "memory", "workspace", "state", "skills", "config", "secrets", "cache", "ignore"}},
			"suggested_category": map[string]any{"type": "string", "enum": []string{"identity", "memory", "workspace", "state", "skills", "config", "secrets"}},
			"secret_tables":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"ref_kind":           map[string]any{"type": "string", "enum": []string{"external_dir", "symlink_target"}},
			"name":               map[string]any{"type": "string"},
			"purpose":            map[string]any{"type": "string"},
			"reason":             map[string]any{"type": "string"},
		}, "kind", "reason")}}, "items")},
}

// Tools implements the agent-kit tools for one instance.
type Tools struct {
	Home     string
	Instance string
	Version  string
	Env      func(context.Context) *porterenroll.Env
	Now      func() time.Time
}

func (t *Tools) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Tools) paths() seal.Paths { return seal.Paths{Home: t.Home} }

func (t *Tools) load() (*enroll.Record, *profile.Profile, error) {
	rec, err := porterenroll.ReadRecord(t.Home, t.Instance)
	if err != nil {
		return nil, nil, err
	}
	if rec == nil {
		return nil, nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+t.Instance, "请用户先运行 valise enroll")
	}
	if rec.Superseded != nil {
		return nil, nil, valise.New("instance_superseded", "实例 "+t.Instance+" 已被实例 "+rec.Superseded.By+" 取代", "请用户检查 MCP 配置中的实例 id")
	}
	p, err := porterenroll.ReadProfile(t.Home, t.Instance)
	return rec, p, err
}

func (t *Tools) audit(op, result, snapshot, reason string) {
	t.paths().AppendAudit(seal.AuditEntry{Time: t.now().UTC(), Op: op, Instance: t.Instance, Initiator: vaultrpc.InitiatorAgentMCP,
		Result: result, SnapshotID: snapshot, Reason: reason})
}

func rateLimited(last, now time.Time, lastID string) error {
	if wait := kit.MinInterval - now.Sub(last); !last.IsZero() && wait > 0 {
		hint := "稍后再试"
		if lastID != "" {
			hint = fmt.Sprintf("最近一次留底为 %s（时间 %s），若覆盖当前修改可直接复用该快照 ID，无需重试",
				lastID, last.UTC().Format(time.RFC3339))
		}
		return valise.New("rate_limited", fmt.Sprintf("距离上一次调用不到 10 分钟，还需等待 %d 分钟", int(wait.Minutes())+1), hint)
	}
	return nil
}

// Call runs one MCP tool. Only the four current tools appear in tools/list;
// hidden legacy tool names stay silently compatible in Call() for v0.1.
func (t *Tools) Call(ctx context.Context, name string, args json.RawMessage) (any, error) {
	switch name {
	case "valise_status":
		return t.status()
	case "valise_seal":
		var a struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(args, &a)
		return t.seal(ctx, a.Reason)
	case "valise_onboard_context":
		return t.onboardContext(ctx)
	case "valise_propose_dossier":
		var a struct {
			Items []kit.DossierProposalItem `json:"items"`
		}
		if err := json.Unmarshal(args, &a); err != nil || len(a.Items) == 0 {
			return nil, valise.New("invalid_arguments", "items 不能为空", "按工具说明传入 kind、reason 及对应字段")
		}
		return t.proposeDossier(a.Items)
	case "valise_propose_enrollment":
		var a struct {
			Items []kit.ProposalItem `json:"items"`
		}
		if err := json.Unmarshal(args, &a); err != nil || len(a.Items) == 0 {
			return nil, valise.New("invalid_arguments", "items 不能为空", "按工具说明传入 root、path、category、reason")
		}
		return t.propose(a.Items)
	case "valise_checkup":
		return t.checkup()
	case "valise_declare":
		var d agentkit.Declaration
		if err := json.Unmarshal(args, &d); err != nil {
			return nil, valise.New("invalid_arguments", "参数格式不对", "按工具说明传入 security_skills 与 approval_mode")
		}
		if _, _, err := t.load(); err != nil {
			return nil, err
		}
		out, err := kit.Declare(t.Home, t.Instance, d, t.now())
		if err != nil {
			return nil, err
		}
		t.audit("declare", "ok", "", "approval_mode="+out.ApprovalMode)
		return map[string]any{"recorded": out, "note": "已记录"}, nil
	case "valise_handoff_begin":
		return t.handoffBegin(ctx)
	case "valise_handoff_finish":
		var a struct {
			SnapshotID string `json:"snapshot_id"`
		}
		_ = json.Unmarshal(args, &a)
		return t.handoffFinish(a.SnapshotID)
	}
	return nil, errUnknownTool
}

type statusPlan struct {
	Adopted         bool     `json:"adopted"`
	Enabled         bool     `json:"enabled"`
	Tier            string   `json:"tier"`
	Schedule        string   `json:"schedule,omitempty"`
	PeriodUnit      string   `json:"period_unit,omitempty"`
	KeepAutoPeriods int      `json:"keep_auto_periods"`
	Targets         []string `json:"targets"`
}

type statusSeal struct {
	SnapshotID string    `json:"snapshot_id"`
	CreatedAt  time.Time `json:"created_at"`
	Tier       string    `json:"tier"`
	Kind       string    `json:"kind,omitempty"`
}

type statusResult struct {
	Instance        string      `json:"instance"`
	ProtectionPlan  statusPlan  `json:"protection_plan"`
	LastSeal        *statusSeal `json:"last_seal,omitempty"`
	Protection      string      `json:"protection_summary"`
	PendingProposal int         `json:"pending_proposals"`
	Notes           []string    `json:"notes,omitempty"`
}

func summarizePlan(p *cp.ServerPlan) statusPlan {
	if p == nil {
		return statusPlan{Adopted: false, Enabled: false, Tier: vault.TierSoul, Targets: []string{}}
	}
	enabled := p.Schedule.Enabled != nil && *p.Schedule.Enabled
	sched := "off"
	if enabled {
		sched = formatPlanSchedule(p.Schedule)
	}
	var targets []string
	for _, sc := range p.ServerCopies {
		if sc.Enabled != nil && *sc.Enabled {
			targets = append(targets, sc.TargetRef.ID)
		}
	}
	if targets == nil {
		targets = []string{}
	}
	return statusPlan{
		Adopted:         true,
		Enabled:         enabled,
		Tier:            p.Scope.Tier,
		Schedule:        sched,
		PeriodUnit:      p.Period.Unit,
		KeepAutoPeriods: p.Period.KeepAutoPeriods,
		Targets:         targets,
	}
}

func formatPlanSchedule(sc cp.Schedule) string {
	var base string
	switch sc.EffectiveCadence() {
	case "hourly":
		base = fmt.Sprintf("every %dh", sc.EveryHours)
		if _, min, ok := strings.Cut(sc.DailyAt, ":"); ok && min != "" && min != "00" {
			base += " at :" + min
		}
	case "weekly":
		base = strings.Join(sc.Weekdays, ",") + " " + sc.DailyAt
	case "monthly":
		base = fmt.Sprintf("day %d %s", sc.DayOfMonth, sc.DailyAt)
	default:
		base = sc.DailyAt
	}
	if sc.Timezone != "" {
		base += " (" + sc.Timezone + ")"
	}
	return base
}

type offsitePod struct {
	SnapshotID string    `json:"snapshot_id"`
	CreatedAt  time.Time `json:"created_at"`
	Targets    []string  `json:"targets"`
}

type protectionRaw struct {
	Targets     []string
	LastSeal    *vault.Meta
	SyncedTo    []string
	LastOffsite *offsitePod
	WaitingSync int
}

// protection is the plain sentence of the protection state.
func protection(r *protectionRaw) string {
	var b strings.Builder
	if len(r.Targets) == 0 {
		b.WriteString("这台服务器没有配置远程备份位置或其他同步位置。")
	} else {
		b.WriteString("已配置 " + strconv.Itoa(len(r.Targets)) + " 个同步位置（" + strings.Join(r.Targets, "、") + "）。")
	}
	switch {
	case r.LastSeal == nil:
		b.WriteString("还没有备份。")
	case len(r.SyncedTo) > 0:
		b.WriteString("最新的备份已经复制到 " + strings.Join(r.SyncedTo, "、") + "。")
	case r.LastOffsite != nil:
		b.WriteString("最新的备份还只在服务器上（" + strconv.Itoa(r.WaitingSync) + " 个在等同步）；别处最新的一份是 " +
			r.LastOffsite.CreatedAt.Local().Format("2006-01-02 15:04") + " 的（" + strings.Join(r.LastOffsite.Targets, "、") + "）。")
	default:
		b.WriteString("备份都只在服务器上，还没有复制到别处。")
	}
	b.WriteString("用户电脑上的本机文件夹由 Valise 客户端管理，这里看不到。")
	return b.String()
}

func (t *Tools) protectionSnapshot() (*statusSeal, string) {
	raw := protectionRaw{SyncedTo: []string{}, Targets: []string{}}
	if vs, err := seal.Status(t.paths()); err == nil {
		for i := range vs.Snapshots {
			m := vs.Snapshots[i]
			if m.InstanceID == t.Instance && (raw.LastSeal == nil || m.CreatedAt.After(raw.LastSeal.CreatedAt)) {
				raw.LastSeal = &m
			}
		}
		if raw.LastSeal != nil {
			for _, tg := range vs.Targets {
				if slices.Contains(tg.LastSynced, raw.LastSeal.SnapshotID) {
					raw.SyncedTo = append(raw.SyncedTo, tg.Name)
				}
			}
		}
		for _, tg := range vs.Targets {
			raw.Targets = append(raw.Targets, tg.Name)
		}
		for _, m := range vs.Snapshots {
			if m.InstanceID != t.Instance {
				continue
			}
			var where []string
			for _, tg := range vs.Targets {
				if slices.Contains(tg.LastSynced, m.SnapshotID) {
					where = append(where, tg.Name)
				}
			}
			switch {
			case len(where) == 0:
				raw.WaitingSync++
			case raw.LastOffsite == nil || m.CreatedAt.After(raw.LastOffsite.CreatedAt):
				raw.LastOffsite = &offsitePod{SnapshotID: m.SnapshotID, CreatedAt: m.CreatedAt, Targets: where}
			}
		}
		if raw.LastOffsite != nil {
			raw.WaitingSync = 0
			for _, m := range vs.Snapshots {
				if m.InstanceID == t.Instance && m.CreatedAt.After(raw.LastOffsite.CreatedAt) {
					raw.WaitingSync++
				}
			}
		}
	}
	var slim *statusSeal
	if raw.LastSeal != nil {
		slim = &statusSeal{
			SnapshotID: raw.LastSeal.SnapshotID,
			CreatedAt:  raw.LastSeal.CreatedAt,
			Tier:       raw.LastSeal.Tier,
			Kind:       raw.LastSeal.Kind,
		}
	}
	return slim, protection(&raw)
}

func (t *Tools) status() (any, error) {
	if _, _, err := t.load(); err != nil {
		return nil, err
	}
	plan, _ := (porterprotection.Store{Home: t.Home}).LoadPlan(t.Instance)
	planSummary := summarizePlan(plan)
	lastSeal, protectionSummary := t.protectionSnapshot()
	res := statusResult{
		Instance:       t.Instance,
		ProtectionPlan: planSummary,
		LastSeal:       lastSeal,
		Protection:     protectionSummary,
	}
	if res.LastSeal == nil {
		res.Notes = append(res.Notes, "这个实例还没有备份过")
	}
	if !planSummary.Adopted {
		res.Notes = append(res.Notes, "当前尚未在 Valise 桌面端确认保护计划；自动留底暂按保守 soul 档处理")
	}
	if ps, err := kit.LoadProposals(t.Home, t.Instance); err == nil {
		res.PendingProposal = len(ps)
	}
	t.audit("status", "ok", "", "")
	return res, nil
}

func (t *Tools) checkup() (any, error) {
	rec, p, err := t.load()
	if err != nil {
		return nil, err
	}
	st, err := kit.LoadState(t.Home, t.Instance)
	if err != nil {
		return nil, err
	}
	now := t.now()
	if err := rateLimited(st.LastCheckup, now, ""); err != nil {
		return nil, err
	}
	res := checkup.Run(rec, p, now)
	st.LastCheckup = now.UTC()
	st.LastFindings = []string{}
	for _, f := range res.Findings {
		st.LastFindings = append(st.LastFindings, f.ID)
	}
	return res, kit.SaveState(t.Home, t.Instance, st)
}

type sealResult struct {
	SnapshotID  string `json:"snapshot_id"`
	Tier        string `json:"tier"`
	Kind        string `json:"kind"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"plain_bytes"`
	PlanWarning string `json:"plan_warning,omitempty"`
	Note        string `json:"note"`
}

func (t *Tools) seal(ctx context.Context, reason string) (any, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "agent mcp backup"
	}
	if _, _, err := t.load(); err != nil {
		return nil, err
	}
	st, err := kit.LoadState(t.Home, t.Instance)
	if err != nil {
		return nil, err
	}
	now := t.now()
	if err := rateLimited(st.LastSeal, now, st.LastSealID); err != nil {
		t.audit("seal", "rate_limited", "", reason)
		return nil, err
	}
	tier := vault.TierSoul
	planWarning := "当前实例尚未确认保护计划，本次仅按保守 soul 档备份（不含凭据和工作区）；建议在 Valise 桌面端确认保护计划以覆盖核心配置与工作区。"
	if plan, _ := (porterprotection.Store{Home: t.Home}).LoadPlan(t.Instance); plan != nil && plan.Scope.Tier != "" {
		tier = plan.Scope.Tier
		planWarning = ""
	}
	rep, err := seal.Seal(ctx, t.Env(ctx), t.paths(), vaultrpc.SealParams{InstanceID: t.Instance, Tier: tier, Reason: reason}, vaultrpc.InitiatorAgentMCP)
	if err != nil {
		return nil, err
	}
	st.LastSeal, st.LastSealID = now.UTC(), rep.SnapshotID
	if err := kit.SaveState(t.Home, t.Instance, st); err != nil {
		return nil, err
	}
	return sealResult{
		SnapshotID:  rep.SnapshotID,
		Tier:        tier,
		Kind:        vault.KindAuto,
		Files:       rep.Files,
		Bytes:       rep.PlainBytes,
		PlanWarning: planWarning,
		Note:        "已建立 " + tier + " 档滚动备份（kind: auto，按日期周期保存）；需要时可在 Valise 客户端恢复或钉为手动存档点",
	}, nil
}

func (t *Tools) proposeDossier(items []kit.DossierProposalItem) (any, error) {
	rec, p, err := t.load()
	if err != nil {
		return nil, err
	}
	if len(items) > 50 {
		return nil, valise.New("too_many_items", "一次最多提议 50 项", "分批提议")
	}
	lines, err := kit.ProposeDossier(t.Home, rec, p, items, t.now())
	if err != nil {
		return nil, err
	}
	t.audit("propose_dossier", "ok", "", "n="+strconv.Itoa(len(items)))
	return map[string]any{"results": lines,
		"next": "请用户在 Valise 客户端「健康 › 归档建议」或运行 valise proposals <服务器> " + t.Instance + " 查看并确认"}, nil
}

func (t *Tools) propose(items []kit.ProposalItem) (any, error) {
	rec, p, err := t.load()
	if err != nil {
		return nil, err
	}
	if len(items) > 50 {
		return nil, valise.New("too_many_items", "一次最多提议 50 项", "分批提议")
	}
	lines, err := kit.Propose(t.Home, rec, p, items, t.now())
	if err != nil {
		return nil, err
	}
	t.audit("propose_enrollment", "ok", "", "n="+strconv.Itoa(len(items)))
	return map[string]any{"results": lines,
		"next": "请用户在 Valise 客户端「健康 › 归档建议」或运行 valise proposals <服务器> " + t.Instance + " 查看并确认"}, nil
}

type handoffBegin struct {
	SnapshotID     string                `json:"snapshot_id"`
	Session        *agentkit.SessionStat `json:"current_session,omitempty"`
	Targets        []kit.TargetBefore    `json:"targets"`
	NewSessionHint string                `json:"new_session_hint,omitempty"`
	Instructions   string                `json:"instructions"`
}

func (t *Tools) handoffBegin(ctx context.Context) (any, error) {
	rec, p, err := t.load()
	if err != nil {
		return nil, err
	}
	targets, err := kit.HandoffTargets(rec, p)
	if err != nil {
		return nil, err
	}
	rep, err := seal.Seal(ctx, t.Env(ctx), t.paths(), vaultrpc.SealParams{InstanceID: t.Instance, Tier: vault.TierSoul, Reason: "handoff"}, vaultrpc.InitiatorAgentMCP)
	if err != nil {
		return nil, err
	}
	st, err := kit.LoadState(t.Home, t.Instance)
	if err != nil {
		return nil, err
	}
	now := t.now()
	st.LastSeal, st.LastSealID = now.UTC(), rep.SnapshotID
	st.Handoff = &kit.HandoffState{SnapshotID: rep.SnapshotID, Started: now.UTC(), Targets: targets}
	if err := kit.SaveState(t.Home, t.Instance, st); err != nil {
		return nil, err
	}
	t.audit("handoff_begin", "ok", rep.SnapshotID, "")
	res := handoffBegin{SnapshotID: rep.SnapshotID, Targets: targets,
		Session: checkup.Run(rec, p, now).Current,
		Instructions: "The old session is sealed and will not be lost. Append ONE section to each target file: first line a Markdown heading " +
			"with today's date (e.g. \"## Handoff " + now.UTC().Format("2006-01-02") + "\"), then only: tasks in progress and next steps, recent facts " +
			"and decisions, open questions. No greetings, no debugging history, no credentials. Do not change anything above the new section. " +
			"Stay within max_lines. Then call valise_handoff_finish with the snapshot_id."}
	if p.Handoff != nil {
		res.NewSessionHint = p.Handoff.NewSessionHint
	}
	return res, nil
}

func (t *Tools) handoffFinish(snapshotID string) (any, error) {
	rec, p, err := t.load()
	if err != nil {
		return nil, err
	}
	st, err := kit.LoadState(t.Home, t.Instance)
	if err != nil {
		return nil, err
	}
	if st.Handoff == nil || st.Handoff.SnapshotID != snapshotID {
		return nil, valise.New("handoff_not_begun", "没有与快照 "+snapshotID+" 对应的交接", "先调用 valise_handoff_begin")
	}
	checks, err := kit.CheckHandoff(rec, st.Handoff)
	if err != nil {
		t.audit("handoff_finish", "check_failed", snapshotID, "")
		return nil, err
	}
	st.Handoff = nil
	if err := kit.SaveState(t.Home, t.Instance, st); err != nil {
		return nil, err
	}
	t.audit("handoff_finish", "ok", snapshotID, "")
	hint := "开启一个新会话；新会话会自动加载交接内容。"
	if p.Handoff != nil && p.Handoff.NewSessionHint != "" {
		hint = p.Handoff.NewSessionHint
	}
	return map[string]any{"checks": checks, "snapshot_id": snapshotID, "next_for_user": hint,
		"note": "旧会话保存在快照 " + snapshotID + " 中"}, nil
}
