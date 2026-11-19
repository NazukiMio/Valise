package service

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"valise/core/action"
	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// The health page (docs/UI.md 9.5) answers two questions: do the escape
// pods really work, and is the agent all right. Each item is one sentence,
// at most one number and one key; problems come first. Plain words only
// (备份, 测试, 恢复). Session and memory findings come from the checkup and
// are added by the caller.

// Health levels, worst last.
const (
	HealthOK   = "ok"
	HealthInfo = "info" // plain text, no colour
	HealthWarn = "warn"
	HealthBad  = "bad"
)

// Health groups.
const (
	HealthPods  = "pods"  // 备份状态
	HealthAgent = "agent" // agent 健康
)

// HealthItem is one line of the health page.
type HealthItem struct {
	ID         string `json:"id"`
	Group      string `json:"group"`
	Level      string `json:"level"`
	Title      string `json:"title"`
	Text       string `json:"text"`
	Key        string `json:"key,omitempty"` // the one key that acts on it
	ActionID   string `json:"action_id,omitempty"`
	ActionName string `json:"action_name,omitempty"`
	// SystemFix names a fix built into Valise (FixLinger, FixClock),
	// planned with PlanSystemFix; ActionName is its name.
	SystemFix string `json:"system_fix,omitempty"`
	Detail    string `json:"detail,omitempty"`
	// Dismissed is set while the user has dismissed it: until then, or
	// the zero time for a yellow item dismissed for good.
	Dismissed *time.Time `json:"dismissed_until,omitempty"`
}

// HealthReport is one agent's health.
type HealthReport struct {
	AgentRef *identity.AgentRef `json:"agent_ref,omitempty"`
	Host     string             `json:"host"`
	Instance string             `json:"instance"`
	Items    []HealthItem       `json:"items"`
	Checks   []HealthCheck      `json:"checks,omitempty"`
}

// Rank orders levels for sorting: bad first.
func Rank(level string) int {
	switch level {
	case HealthBad:
		return 0
	case HealthWarn:
		return 1
	case HealthInfo:
		return 2
	}
	return 3
}

// Problems counts warn and bad items.
func (r *HealthReport) Problems() int {
	n := 0
	for _, it := range r.Items {
		if it.Counts() {
			n++
		}
	}
	return n
}

// Sort puts problems first within each group, keeping the group order.
func (r *HealthReport) Sort() {
	sort.SliceStable(r.Items, func(i, j int) bool {
		a, b := r.Items[i], r.Items[j]
		if a.Group != b.Group {
			return a.Group == HealthPods
		}
		return sortRank(a) < sortRank(b)
	})
}

// sortRank puts a dismissed warn after the plain items.
func sortRank(it HealthItem) int {
	if it.Level == HealthWarn && it.Dismissed != nil {
		return 4
	}
	return Rank(it.Level)
}

const clockSkewLimit = 5 * time.Minute

// Health collects independent checks; failure of one never hides another.
func (s *Service) Health(ctx context.Context, host, instance string) (*HealthReport, error) {
	var checked *action.ApprovalResult
	report := s.collectHealth(host, instance, healthSources{
		vault:     func() (*vaultrpc.StatusResult, error) { return s.VaultStatus(ctx, host) },
		instances: func() ([]enroll.InstanceSummary, error) { return s.Instances(ctx, host) },
		timer:     func() (*vaultrpc.TimerStatus, error) { return s.TimerStatus(ctx, host, instance) },
		kit:       func() (*agentkit.KitStatusResult, error) { return s.KitStatus(ctx, host, instance) },
		system:    func() (*systemRead, error) { return s.readSystem(ctx, host, instance) },
		approval: func() (*action.ApprovalResult, error) {
			var chk action.ApprovalResult
			err := s.call(ctx, host, action.MethodApproval, action.ApprovalParams{InstanceID: instance}, &chk)
			if err == nil {
				checked = &chk
			}
			return &chk, err
		},
	})
	if checked != nil && checked.Declared && !checked.Pending && checked.Mode == profile.ApprovalBypass {
		s.bindApprovalRepair(ctx, host, instance, report)
	}
	return report, nil
}

func (s *Service) collectHealth(host, instance string, src healthSources) *HealthReport {
	r := &HealthReport{Host: host, Instance: instance, Items: []HealthItem{}}
	add := func(it HealthItem) { r.Items = append(r.Items, it) }
	now := time.Now()
	var vaultNow time.Time
	st, err := src.vault()
	r.read("backup", "备份与服务器时间", err)
	if err == nil {
		add(s.backupItem(host, instance, st.ServerName, st.Snapshots, st.Targets, now))
		vaultNow = st.Now
	}
	var currentAgent *identity.AgentRef
	profileID := ""
	insts, err := src.instances()
	found := false
	if err == nil {
		for _, in := range insts {
			if in.InstanceID == instance {
				if in.Identity != nil && in.Identity.Agent != nil {
					ref := in.Identity.Agent.Ref
					currentAgent = &ref
				}
				it := serviceItem(in.Service, in.Entries)
				s.bindServiceRepair(&it, in)
				add(it)
				profileID = in.ProfileID
				found = true
				break
			}
		}
		if !found {
			err = errors.New("instance not listed")
		}
	}
	r.read("service", "服务与管理页", err)
	r.AgentRef = currentAgent
	add(s.restoreTestItem(host, instance, now, currentAgent))
	ts, err := src.timer()
	r.read("timer", "定时备份", err)
	legacyTimer, legacyLinger := false, false
	if err == nil {
		legacyTimer, legacyLinger = ts.Enabled, ts.Linger
		it := HealthItem{ID: "timer", Group: HealthAgent, Title: "定时备份"}
		switch {
		case !ts.Enabled:
			it.Level, it.Text, it.Key = HealthInfo, "没有开启", "s"
			it.Detail = "定时备份让你不用记着手动备份。按 s 打开这个 agent 的设置，在「定时备份」里开启。"
		case ts.Every == "":
			// A protection plan's calendar schedule has no fixed interval.
			it.Level, it.Text = HealthOK, "按保护计划的时间运行，登出后也会运行"
		default:
			it.Level, it.Text = HealthOK, "每 "+everyText(ts.Every)+"一次，登出后也会运行"
		}
		add(it)
	}
	k, err := src.kit()
	r.read("kit", "Agent 保护技能", err)
	if err == nil {
		stale := 0
		for _, sk := range k.Skills {
			if sk.State != agentkit.SkillCurrent {
				stale++
			}
		}
		if stale > 0 {
			add(HealthItem{ID: "skills", Group: HealthAgent, Level: HealthInfo, Title: "Agent 保护技能", Key: "s",
				Text:   strconv.Itoa(stale) + " 个不是这个版本的（未安装或被改过）",
				Detail: "按 s 打开这个 agent 的设置，在「Agent 保护技能」里重新安装。"})
		}
	}
	chk, err := src.approval()
	r.read("approval", "审批设置", err)
	if err == nil {
		switch {
		case !chk.Declared:
			r.Checks[len(r.Checks)-1].State = "unsupported"
			r.Checks[len(r.Checks)-1].Reason = "harness 资料未声明审批检查"
		case chk.Mode == profile.ApprovalUnknown:
			r.Checks[len(r.Checks)-1].State = "failed"
			r.Checks[len(r.Checks)-1].Reason = "未能判定审批设置，请检查 harness 资料与服务器上的程序版本后刷新"
			chk = nil // never expose raw failed command details
		}
	} else {
		chk = nil
	}
	it := approvalItem(chk)
	if r.Checks[len(r.Checks)-1].State == "failed" && it.Level == HealthOK {
		it.Level = HealthInfo
	}
	if it.Level == HealthBad {
		it.Detail += s.approvalRemedy(profileID)
	}
	add(it)
	s.systemItems(r, host, instance, src.system, legacyTimer, legacyLinger, vaultNow)
	lingerNote(r)
	s.MarkDismissed(host, instance, r.Items)
	r.Sort()
	return r
}

// backupItem: the newest escape pod, and the newest one kept in a
// repository or on a station (the server's sync record; a restore-ready
// check is the advanced-feature gate's job).
func (s *Service) backupItem(host, instance, server string, metas []vault.Meta, targets []vaultrpc.TargetState, now time.Time) HealthItem {
	it := HealthItem{ID: "backup", Group: HealthPods, Title: "备份"}
	var pods []vault.Meta
	for _, m := range metas {
		if m.InstanceID == instance {
			pods = append(pods, m)
		}
	}
	if len(pods) == 0 {
		it.Level, it.Text = HealthBad, "还没有备份"
		it.Detail = "出了问题没有可以恢复的东西。先建立一个备份（推荐\"记忆与人格 + 凭据\"），再复制到备份位置。"
		return it
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].CreatedAt.After(pods[j].CreatedAt) })
	elsewhere := map[string]string{}
	for _, rs := range s.RepoStatuses(host, server) {
		if !rs.Available {
			continue
		}
		for _, ids := range rs.Leaves {
			for _, id := range ids {
				elsewhere[id] = "仓库 " + rs.Name
			}
		}
	}
	for _, t := range targets {
		for _, id := range t.LastSynced {
			if _, ok := elsewhere[id]; !ok {
				elsewhere[id] = "备份位置 " + t.Name
			}
		}
	}
	newest := "最新备份 " + agoAt(pods[0].CreatedAt, now)
	for i, m := range pods {
		where, ok := elsewhere[m.SnapshotID]
		if !ok {
			continue
		}
		age := now.Sub(m.CreatedAt)
		it.Text = newest + "；备份位置里最新的是 " + agoAt(m.CreatedAt, now) + "（" + where + "）"
		it.Level = HealthOK
		if i > 0 {
			it.Text += "，还有 " + strconv.Itoa(i) + " 个没同步"
			it.Key = "u"
		}
		if age > staleAfter {
			it.Level = HealthWarn
			it.Detail = "服务器出问题时，只能用备份位置里的备份恢复：会丢掉这 " + agoPlain(age) + "的记忆、会话与配置改动。按 u 打开「备份位置」，在那里复制过去（只追加，不改动已有的文件）。"
		}
		return it
	}
	it.Level, it.Key = HealthWarn, "u"
	it.Text = newest + "，但都只在服务器上"
	it.Detail = "服务器坏了或被删光时，只在服务器上的备份会一起丢失。按 u 打开「备份位置」：还没有就先添加一个（本机文件夹就可以），然后复制过去。"
	return it
}

func (s *Service) restoreTestItem(host, instance string, now time.Time, current *identity.AgentRef) HealthItem {
	it := HealthItem{ID: "restore_test", Group: HealthPods, Title: "备份文件测试", Key: "t"}
	t, ok := s.LastRestoreTest(host, instance)
	switch {
	case !ok:
		it.Level, it.Text = HealthWarn, "还没有测试过"
		it.Detail = "选择要测试的备份和读取位置，在这台电脑和服务器上分别核对文件；不切换当前 Agent，结束后清理暂存。需要 Valise 口令。"
	case restoreTestPending(t):
		it.Level, it.Text = HealthWarn, "上次测试尚未收尾，结果未核实"
		it.Detail = "先核实并清理原测试的暂存；不会重新测试或替换当前 Agent。"
	case t.AgentRef != nil && (current == nil || *t.AgentRef != *current):
		it.Level, it.Text = HealthWarn, "还没有当前实例世代的文件测试结果"
		it.Detail = "已有测试属于另一个实例世代，或当前身份暂未读到；重新检查连接后再测试。"
	case t.AgentRef == nil && current != nil:
		it.Level, it.Text = HealthInfo, "有旧的文件测试记录，尚未核实与当前 Agent 的关联"
		it.Detail = "可以重新测试当前备份。"
	case t.State == "cancelled":
		it.Level, it.Text = HealthWarn, "上次测试已停止，暂存已清理"
	case !t.OK:
		it.Level = HealthBad
		it.Text = "上次测试 " + agoPlain(now.Sub(t.At)) + "前，没有通过：" + t.Problem
		it.Detail = "可以换一个更早的备份再测试；如果更早的也不行，请报告问题。"
	default:
		it.Level = HealthOK
		it.Text = "上次文件与暂存校验 " + agoPlain(now.Sub(t.At)) + "前通过（" + t.Created.Local().Format("01-02 15:04") + " 的备份，" + strconv.Itoa(t.Files) + " 个文件）；未测试启动"
	}
	return it
}

func serviceItem(svc []enroll.ServiceState, entries []enroll.EntryState) HealthItem {
	it := HealthItem{ID: "service", Group: HealthAgent, Title: "服务与管理页", Level: HealthOK}
	var parts []string
	for _, u := range svc {
		if !u.Exists() {
			continue
		}
		if u.Active == "active" {
			parts = append(parts, "运行中")
		} else {
			it.Level = HealthBad
			parts = append(parts, u.Unit+" 没有运行（"+u.Active+"）")
		}
	}
	for _, e := range entries {
		if e.Listening {
			parts = append(parts, "管理页可以打开")
		} else {
			if it.Level == HealthOK {
				it.Level = HealthWarn
			}
			parts = append(parts, "管理页没有响应（端口 "+strconv.Itoa(e.Port)+"）")
		}
	}
	if len(parts) == 0 {
		it.Level, it.Text = HealthInfo, "harness 资料没有声明服务或管理页"
		return it
	}
	it.Text = strings.Join(parts, " · ")
	var down []string
	for _, u := range svc {
		if u.Exists() && u.Active != "active" {
			down = append(down, u.Unit)
		}
	}
	if len(down) > 0 {
		// The agent's own service: Valise says how, the user does it.
		it.Detail = "可以在「管理」里用 harness 资料登记的重启动作，或在服务器终端里运行：systemctl --user restart " + strings.Join(down, " ") +
			"；还起不来时看日志：journalctl --user -u " + down[0] + " -n 50"
	}
	return it
}

// approvalItem renders the harness's deterministic approval_check setting.
func approvalItem(chk *action.ApprovalResult) HealthItem {
	it := HealthItem{ID: "approval", Group: HealthAgent, Title: "审批"}
	bypassDetail := "agent 可以不经你同意删除文件、改配置或运行命令；被注入的指令也会直接执行。要改的话，可在 harness 的审批设置中改为执行前询问；本版本尚未提供审批设置的执行入口，修改后刷新复核；不改也可以按 i 忽略。"
	if chk == nil || !chk.Declared || chk.Mode == profile.ApprovalUnknown {
		it.Level, it.Text = HealthInfo, "未能从 harness 设置判定审批模式"
		return it
	}
	src := "harness 设置读出"
	if chk.Pending {
		src += "，读法尚未核实"
	}
	src += "：" + chk.Detail
	switch chk.Mode {
	case profile.ApprovalBypass:
		it.Level, it.Text, it.Detail = HealthBad, "agent 执行风险命令无需批准（Bypass）（"+src+"）", bypassDetail
		if chk.Default {
			it.Detail = "这是 harness 的出厂设置，不是你改的。" + bypassDetail
		}
	default:
		it.Level, it.Text = HealthOK, "执行风险命令前需要你批准（"+src+"）"
	}
	return it
}

// agoAt says how long ago t was; a time after now (recorded while the
// server's clock ran ahead) is told as the date it carries, never as “just
// now”.
func agoAt(t, now time.Time) string {
	if t.After(now.Add(time.Minute)) {
		return "记为 " + t.Local().Format("2006-01-02 15:04") + "，晚于现在（备份时服务器时钟可能不准）"
	}
	return agoPlain(now.Sub(t)) + "前"
}

func agoPlain(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + " 天"
	case d >= time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + " 小时"
	}
	return strconv.Itoa(max(1, int(d/time.Minute))) + " 分钟"
}

// everyText turns a timer interval ("6h", "1d", "30m") into words.
func everyText(every string) string {
	if n := len(every); n > 1 {
		if _, err := strconv.Atoi(every[:n-1]); err == nil {
			switch every[n-1] {
			case 'd':
				return every[:n-1] + " 天"
			case 'h':
				return every[:n-1] + " 小时"
			case 'm':
				return every[:n-1] + " 分钟"
			}
		}
	}
	return every + " "
}

// approvalRemedy is the harness profile's own steps to turn approval on, for
// the user to follow until a verified executable action is implemented.
func (s *Service) approvalRemedy(profileID string) string {
	set, err := s.Profiles()
	if err != nil || set.ByID[profileID] == nil {
		return ""
	}
	c := set.ByID[profileID].Effective.ApprovalCheck
	if c == nil || len(c.Remedy) == 0 {
		return ""
	}
	return "\n怎么改（" + set.ByID[profileID].Effective.DisplayName + "）：\n  " + strings.Join(c.Remedy, "\n  ")
}
