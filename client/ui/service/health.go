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
	HealthPods  = "pods"  // 逃生舱状态
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
	Detail     string `json:"detail,omitempty"`
	// Dismissed is set while the user has dismissed it: until then, or
	// the zero time for a yellow item dismissed for good.
	Dismissed *time.Time `json:"dismissed_until,omitempty"`
}

// HealthReport is one agent's health.
type HealthReport struct {
	Host     string        `json:"host"`
	Instance string        `json:"instance"`
	Items    []HealthItem  `json:"items"`
	Checks   []HealthCheck `json:"checks,omitempty"`
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
	return s.collectHealth(host, instance, healthSources{
		vault:     func() (*vaultrpc.StatusResult, error) { return s.VaultStatus(ctx, host) },
		instances: func() ([]enroll.InstanceSummary, error) { return s.Instances(ctx, host) },
		timer:     func() (*vaultrpc.TimerStatus, error) { return s.TimerStatus(ctx, host, instance) },
		kit:       func() (*agentkit.KitStatusResult, error) { return s.KitStatus(ctx, host, instance) },
		approval: func() (*action.ApprovalResult, error) {
			var chk action.ApprovalResult
			err := s.call(ctx, host, action.MethodApproval, action.ApprovalParams{InstanceID: instance}, &chk)
			return &chk, err
		},
	}), nil
}

func (s *Service) collectHealth(host, instance string, src healthSources) *HealthReport {
	r := &HealthReport{Host: host, Instance: instance, Items: []HealthItem{}}
	add := func(it HealthItem) { r.Items = append(r.Items, it) }
	now := time.Now()
	st, err := src.vault()
	r.read("backup", "备份与服务器时间", err)
	if err == nil {
		add(s.backupItem(host, instance, st.ServerName, st.Snapshots, st.Targets, now))
		if !st.Now.IsZero() {
			if skew := st.Now.Sub(now); skew > clockSkewLimit || skew < -clockSkewLimit {
				if skew < 0 {
					skew = -skew
				}
				add(HealthItem{ID: "clock", Group: HealthAgent, Level: HealthWarn, Title: "服务器时钟",
					Text:   "和这台电脑差 " + agoPlain(skew),
					Detail: "时钟偏差会让空间站的短期证书失效，也会让逃生舱的时间显示不准。在服务器上开启时间同步（如 chrony 或 systemd-timesyncd）。"})
			}
		}
	}
	add(s.restoreTestItem(host, instance, now))
	profileID := ""
	insts, err := src.instances()
	found := false
	if err == nil {
		for _, in := range insts {
			if in.InstanceID == instance {
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
	ts, err := src.timer()
	r.read("timer", "定时备份", err)
	if err == nil {
		it := HealthItem{ID: "timer", Group: HealthAgent, Title: "定时备份"}
		switch {
		case !ts.Enabled:
			it.Level, it.Text, it.Key = HealthInfo, "没有开启", "s"
			it.Detail = "定时建立逃生舱让备份不靠你记得。按 s 打开这个 agent 的设置，在「定时建立逃生舱」里开启。"
		case !ts.Linger:
			it.Level, it.Text, it.Key = HealthWarn, "每 "+everyText(ts.Every)+"一次，但 linger 没有开启：你登出或服务器重启后会停下", "k"
			it.Detail = "linger 让服务器在你没有登录时也运行你的定时任务。按 k 打开 Kit 工具，在那里开启（专家级，需要高级模式）；也可以自己在服务器终端里运行 sudo loginctl enable-linger $USER。"
		default:
			it.Level, it.Text = HealthOK, "每 "+everyText(ts.Every)+"一次，登出后也会运行"
		}
		add(it)
	}
	k, err := src.kit()
	r.read("kit", "逃生舱技能", err)
	var declaration *agentkit.Declaration
	if err == nil {
		declaration = k.Declaration
		stale := 0
		for _, sk := range k.Skills {
			if sk.State != agentkit.SkillCurrent {
				stale++
			}
		}
		if stale > 0 {
			add(HealthItem{ID: "skills", Group: HealthAgent, Level: HealthInfo, Title: "逃生舱技能", Key: "s",
				Text:   strconv.Itoa(stale) + " 个不是这个版本的（未安装或被改过）",
				Detail: "按 s 打开这个 agent 的设置，在「逃生舱技能」里重新安装。"})
		}
	}
	chk, err := src.approval()
	r.read("approval", "审批设置", err)
	if err == nil {
		switch {
		case !chk.Declared:
			r.Checks[len(r.Checks)-1].State = "unsupported"
			r.Checks[len(r.Checks)-1].Reason = "harness 资料未声明审批检查；仅展示 agent 声明（如有）"
		case chk.Mode == profile.ApprovalUnknown:
			r.Checks[len(r.Checks)-1].State = "failed"
			r.Checks[len(r.Checks)-1].Reason = "未能判定审批设置，请检查 harness 资料与服务器上的程序版本后刷新"
			chk = nil // never expose raw failed command details or turn a declaration green
		}
	} else {
		chk = nil
	}
	it := approvalItem(chk, declaration)
	if r.Checks[len(r.Checks)-1].State == "failed" && it.Level == HealthOK {
		it.Level = HealthInfo
	}
	if it.Level == HealthBad {
		it.Detail += s.approvalRemedy(profileID)
	}
	add(it)
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
		it.Level, it.Text = HealthBad, "还没有逃生舱"
		it.Detail = "出了问题没有可以恢复的东西。先建立一个逃生舱（推荐\"记忆与人格 + 凭据\"），再复制到仓库或空间站。"
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
				elsewhere[id] = "空间站 " + t.Name
			}
		}
	}
	newest := "最新逃生舱 " + agoPlain(now.Sub(pods[0].CreatedAt)) + "前"
	for i, m := range pods {
		where, ok := elsewhere[m.SnapshotID]
		if !ok {
			continue
		}
		age := now.Sub(m.CreatedAt)
		it.Text = newest + "；仓库或空间站里最新的是 " + agoPlain(age) + "前（" + where + "）"
		it.Level = HealthOK
		if i > 0 {
			it.Text += "，还有 " + strconv.Itoa(i) + " 个没同步"
			it.Key = "u"
		}
		if age > staleAfter {
			it.Level = HealthWarn
			it.Detail = "服务器出问题时，只能用仓库或空间站里的备份恢复：会丢掉这 " + agoPlain(age) + "的记忆、会话与配置改动。按 u 打开「仓库与空间站」，在那里复制过去（只追加，不改动已有的文件）。"
		}
		return it
	}
	it.Level, it.Key = HealthWarn, "u"
	it.Text = newest + "，但都只在服务器上"
	it.Detail = "服务器坏了或被删光时，只在服务器上的逃生舱会一起丢失。按 u 打开「仓库与空间站」：还没有就先添加一个（本机文件夹就可以），然后复制过去。"
	return it
}

func (s *Service) restoreTestItem(host, instance string, now time.Time) HealthItem {
	it := HealthItem{ID: "restore_test", Group: HealthPods, Title: "恢复测试", Key: "t"}
	t, ok := s.LastRestoreTest(host, instance)
	switch {
	case !ok:
		it.Level, it.Text = HealthWarn, "还没有测试过"
		it.Detail = "恢复测试在这台电脑和服务器上各解开一次最新的逃生舱并逐个核对文件，不切换、不碰 agent，结束后删除临时文件。需要 Valise 口令。"
	case !t.OK:
		it.Level = HealthBad
		it.Text = "上次测试 " + agoPlain(now.Sub(t.At)) + "前，没有通过：" + t.Problem
		it.Detail = "可以换一个更早的逃生舱再测试；如果更早的也不行，请报告问题。"
	default:
		it.Level = HealthOK
		it.Text = "上次测试 " + agoPlain(now.Sub(t.At)) + "前，通过（" + t.Created.Local().Format("01-02 15:04") + " 的逃生舱，" + strconv.Itoa(t.Files) + " 个文件）"
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

// approvalItem: the harness's own settings (the profile's approval_check)
// come first; the agent's declaration fills in when there is no check, and
// is shown against the settings when they disagree.
func approvalItem(chk *action.ApprovalResult, d *agentkit.Declaration) HealthItem {
	it := HealthItem{ID: "approval", Group: HealthAgent, Title: "审批"}
	bypassDetail := "agent 可以不经你同意删除文件、改配置或运行命令；被注入的指令也会直接执行。要改的话，可在 harness 的审批设置中改为执行前询问；本版本尚未提供审批设置的执行入口，修改后刷新复核；不改也可以按 i 忽略。"
	declared := ""
	if d != nil {
		declared = d.ApprovalMode
	}
	if chk != nil && chk.Declared && chk.Mode != profile.ApprovalUnknown {
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
			if declared == agentkit.ApprovalAsk {
				it.Text += "；agent 声称需要批准，与设置不符"
			}
		default:
			it.Level, it.Text = HealthOK, "执行风险命令前需要你批准（"+src+"）"
			if declared == agentkit.ApprovalBypass {
				it.Level = HealthWarn
				it.Text += "；agent 声称无需批准，与设置不符"
				it.Detail = "可能还有设置没被读到（例如某个会话里临时放开）。让 agent 说明它在哪里看到自己无需批准。"
			}
		}
		return withSkills(it, d)
	}
	switch {
	case d == nil:
		it.Level, it.Text = HealthInfo, "agent 还没有声明执行风险命令前是否需要你批准"
	case d.ApprovalMode == agentkit.ApprovalBypass:
		it.Level, it.Text, it.Detail = HealthBad, "agent 执行风险命令无需批准（Bypass）（agent 声明，未经核实）", bypassDetail
	case d.ApprovalMode == agentkit.ApprovalAsk:
		it.Level, it.Text = HealthOK, "执行风险命令前需要你批准（agent 声明，未经核实）"
	default:
		it.Level, it.Text = HealthInfo, "agent 说不清执行风险命令前是否需要你批准"
	}
	return withSkills(it, d)
}

func withSkills(it HealthItem, d *agentkit.Declaration) HealthItem {
	if d != nil && len(d.SecuritySkills) > 0 {
		var names []string
		for _, sk := range d.SecuritySkills {
			names = append(names, sk.Name)
		}
		it.Text += "；声明的安全技能：" + strings.Join(names, "、")
	}
	return it
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
