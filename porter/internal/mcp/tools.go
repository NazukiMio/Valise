package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/porter/internal/checkup"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/kit"
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
	{"name": "valise_status", "description": "Show how this instance is protected: last seal (time, tier), whether it is synced off-site, " +
		"current checkup findings, and classification hints (unclassified paths; files in identity/config/skills that change on every seal). Paths and counts only, never file contents.",
		"inputSchema": schema(map[string]any{})},
	{"name": "valise_checkup", "description": "Run the code-only memory checkup and return metrics and finding ids (long_session, cost_growth, memory_bloat, session_pileup, no_memory_mechanism). At most once every 10 minutes.",
		"inputSchema": schema(map[string]any{})},
	{"name": "valise_seal", "description": "Seal the soul tier (persona, memory, skills, sessions, state; no credentials) into the vault. " +
		"Call this FIRST, before you: save, edit or delete any memory (including remembering a new fact about the user); edit your persona, rules or " +
		"instructions; install or remove skills or plugins; change configuration; upgrade the harness; or edit/clean up many files. " +
		"At most once every 10 minutes (if you sealed minutes ago for the same work, continue). The reason is written to the audit log.",
		"inputSchema": schema(map[string]any{"reason": map[string]any{"type": "string", "description": "What you are about to change and why"}}, "reason")},
	{"name": "valise_propose_enrollment", "description": "Propose how files this instance uses should be classified (e.g. a notes directory the user asked you to keep). Code validates each item; the user confirms in the Valise client. " +
		"Categories: identity, memory, skills, sessions, state, config, workspace, secrets, cache, body, ignore. Moving files to cache/body/ignore takes them out of the soul tier and needs separate user confirmation.",
		"inputSchema": schema(map[string]any{"items": map[string]any{"type": "array", "items": schema(map[string]any{
			"root":     map[string]any{"type": "string", "description": "Root id, e.g. state or workspace"},
			"path":     map[string]any{"type": "string", "description": "Path relative to the root (file or directory)"},
			"category": map[string]any{"type": "string"},
			"reason":   map[string]any{"type": "string"},
		}, "root", "path", "category", "reason")}}, "items")},
	{"name": "valise_handoff_begin", "description": "Start a handoff to a new session: seals the soul tier (including this session), then returns the snapshot id, current session statistics, the memory files to append the handoff to, their line limits and how the user starts a new session.",
		"inputSchema": schema(map[string]any{})},
	{"name": "valise_handoff_finish", "description": "Finish a handoff after appending one dated section to the handoff target(s). Code checks that only an appended section changed and the line limit holds.",
		"inputSchema": schema(map[string]any{"snapshot_id": map[string]any{"type": "string"}}, "snapshot_id")},
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
		return nil, nil, valise.New("instance_not_enrolled", "服务器上没有登记实例 "+t.Instance, "请用户先运行 valise enroll")
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

func rateLimited(last, now time.Time) error {
	if wait := kit.MinInterval - now.Sub(last); !last.IsZero() && wait > 0 {
		return valise.New("rate_limited", fmt.Sprintf("距离上一次调用不到 10 分钟，还需等待 %d 分钟", int(wait.Minutes())+1),
			"稍后再试；如果刚封存过，可以直接继续操作")
	}
	return nil
}

// Call runs one tool.
func (t *Tools) Call(ctx context.Context, name string, args json.RawMessage) (any, error) {
	switch name {
	case "valise_status":
		return t.status(ctx)
	case "valise_checkup":
		return t.checkup()
	case "valise_seal":
		var a struct {
			Reason string `json:"reason"`
		}
		json.Unmarshal(args, &a)
		return t.seal(ctx, a.Reason)
	case "valise_propose_enrollment":
		var a struct {
			Items []kit.ProposalItem `json:"items"`
		}
		if err := json.Unmarshal(args, &a); err != nil || len(a.Items) == 0 {
			return nil, valise.New("invalid_arguments", "items 不能为空", "按工具说明传入 root、path、category、reason")
		}
		return t.propose(a.Items)
	case "valise_handoff_begin":
		return t.handoffBegin(ctx)
	case "valise_handoff_finish":
		var a struct {
			SnapshotID string `json:"snapshot_id"`
		}
		json.Unmarshal(args, &a)
		return t.handoffFinish(a.SnapshotID)
	}
	return nil, errUnknownTool
}

type statusResult struct {
	Instance        string                     `json:"instance"`
	Profile         string                     `json:"profile"`
	ProfileStatus   string                     `json:"profile_status"`
	LastSeal        *vault.Meta                `json:"last_seal,omitempty"`
	SyncedTo        []string                   `json:"last_seal_synced_to"`
	Findings        []string                   `json:"findings"`
	LastCheckup     *time.Time                 `json:"last_checkup,omitempty"`
	Unclassified    []enroll.UnclassifiedGroup `json:"unclassified"`
	ChangingStable  []string                   `json:"changing_stable_files"`
	PendingProposal int                        `json:"pending_proposals"`
	HandoffOpen     string                     `json:"handoff_open_snapshot,omitempty"`
	Notes           []string                   `json:"notes,omitempty"`
}

func (t *Tools) status(ctx context.Context) (any, error) {
	rec, p, err := t.load()
	if err != nil {
		return nil, err
	}
	st, err := kit.LoadState(t.Home, t.Instance)
	if err != nil {
		return nil, err
	}
	res := statusResult{Instance: t.Instance, Profile: fmt.Sprintf("%s v%d", p.ID, p.ProfileVersion), ProfileStatus: p.Status,
		SyncedTo: []string{}, Findings: append([]string{}, st.LastFindings...), Unclassified: []enroll.UnclassifiedGroup{}, ChangingStable: []string{}}
	if !st.LastCheckup.IsZero() {
		lc := st.LastCheckup
		res.LastCheckup = &lc
	}
	if vs, err := seal.Status(t.paths()); err == nil {
		for i := range vs.Snapshots {
			m := vs.Snapshots[i]
			if m.InstanceID == t.Instance && (res.LastSeal == nil || m.CreatedAt.After(res.LastSeal.CreatedAt)) {
				res.LastSeal = &m
			}
		}
		if res.LastSeal != nil {
			for _, tg := range vs.Targets {
				if slices.Contains(tg.LastSynced, res.LastSeal.SnapshotID) {
					res.SyncedTo = append(res.SyncedTo, tg.Name)
				}
			}
		}
	}
	if res.LastSeal == nil {
		res.Notes = append(res.Notes, "这个实例还没有封存过")
	}
	rep, err := porterenroll.Scan(ctx, t.Env(ctx), enroll.ScanParams{Profile: *p, StateDir: rec.Roots[profile.StateRoot],
		InstanceID: t.Instance, Overrides: rec.LocalOverrides}, rec)
	if err == nil {
		res.Unclassified = rep.UnclassGroups
		if len(res.Unclassified) > 20 {
			res.Unclassified = res.Unclassified[:20]
		}
	}
	for path, e := range seal.ReadSeen(t.paths(), t.Instance) {
		if e.Streak >= 2 {
			res.ChangingStable = append(res.ChangingStable, path)
		}
	}
	slices.Sort(res.ChangingStable)
	if ps, err := kit.LoadProposals(t.Home, t.Instance); err == nil {
		res.PendingProposal = len(ps)
	}
	if st.Handoff != nil && !st.Handoff.Stale(t.now()) {
		res.HandoffOpen = st.Handoff.SnapshotID
	}
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
	if err := rateLimited(st.LastCheckup, now); err != nil {
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
	SnapshotID string `json:"snapshot_id"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"plain_bytes"`
	Note       string `json:"note"`
}

func (t *Tools) seal(ctx context.Context, reason string) (any, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, valise.New("reason_required", "封存需要写明原因", "在 reason 中写明你将要做的修改")
	}
	if _, _, err := t.load(); err != nil {
		return nil, err
	}
	st, err := kit.LoadState(t.Home, t.Instance)
	if err != nil {
		return nil, err
	}
	now := t.now()
	if err := rateLimited(st.LastSeal, now); err != nil {
		t.audit("seal", "rate_limited", "", reason)
		return nil, err
	}
	rep, err := seal.Seal(ctx, t.Env(ctx), t.paths(), vaultrpc.SealParams{InstanceID: t.Instance, Tier: vault.TierSoul, Reason: reason}, vaultrpc.InitiatorAgentMCP)
	if err != nil {
		return nil, err
	}
	st.LastSeal = now.UTC()
	return sealResult{SnapshotID: rep.SnapshotID, Files: rep.Files, Bytes: rep.PlainBytes,
		Note: "已封存 soul 档（不含凭据）；需要时用户可以在客户端用这个快照复活或回滚"}, kit.SaveState(t.Home, t.Instance, st)
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
	return map[string]any{"results": lines,
		"next": "请用户在 Valise 客户端运行 valise proposals <服务器> " + t.Instance + " 查看并确认"}, nil
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
	st.LastSeal = now.UTC()
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
