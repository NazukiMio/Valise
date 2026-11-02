package service

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"valise/core/agentkit"
	"valise/core/enroll"
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
	ID     string `json:"id"`
	Group  string `json:"group"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Text   string `json:"text"`
	Key    string `json:"key,omitempty"` // the one key that acts on it
	Detail string `json:"detail,omitempty"`
	// Dismissed is set while the user has dismissed it (until then).
	Dismissed *time.Time `json:"dismissed_until,omitempty"`
}

// HealthReport is one agent's health.
type HealthReport struct {
	Host     string       `json:"host"`
	Instance string       `json:"instance"`
	Items    []HealthItem `json:"items"`
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

// Health collects instance's health on host. Nothing is asked and nothing
// changes; a part that cannot be read is left out.
func (s *Service) Health(ctx context.Context, host, instance string) (*HealthReport, error) {
	st, err := s.VaultStatus(ctx, host)
	if err != nil {
		return nil, err
	}
	r := &HealthReport{Host: host, Instance: instance, Items: []HealthItem{}}
	add := func(it HealthItem) { r.Items = append(r.Items, it) }
	now := time.Now()

	add(s.backupItem(host, instance, st.ServerName, st.Snapshots, st.Targets, now))
	add(s.restoreTestItem(host, instance, now))

	if insts, err := s.Instances(ctx, host); err == nil {
		for _, in := range insts {
			if in.InstanceID == instance {
				add(serviceItem(in.Service, in.Entries))
			}
		}
	}
	if ts, err := s.TimerStatus(ctx, host, instance); err == nil {
		it := HealthItem{ID: "timer", Group: HealthAgent, Title: "定时备份"}
		switch {
		case !ts.Enabled:
			it.Level, it.Text = HealthInfo, "没有开启（在设置里开启）"
		case !ts.Linger:
			it.Level, it.Text = HealthWarn, "每 "+ts.Every+" 一次，但 linger 没有开启：你登出或服务器重启后会停下"
			it.Detail = "linger 让服务器在你没有登录时也运行你的定时任务。开启需要管理员权限：在服务器终端里运行 sudo loginctl enable-linger $USER（Kit 专家级会提供一键开启）。"
		default:
			it.Level, it.Text = HealthOK, "每 "+ts.Every+" 一次，登出后也会运行"
		}
		add(it)
	}
	if k, err := s.KitStatus(ctx, host, instance); err == nil {
		add(approvalItem(k.Declaration))
		stale := 0
		for _, sk := range k.Skills {
			if sk.State != agentkit.SkillCurrent {
				stale++
			}
		}
		if stale > 0 {
			add(HealthItem{ID: "skills", Group: HealthAgent, Level: HealthInfo, Title: "逃生舱技能",
				Text: strconv.Itoa(stale) + " 个不是这个版本的（未安装或被改过），在设置里重新安装"})
		}
	}
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
	s.MarkDismissed(host, instance, r.Items)
	r.Sort()
	return r, nil
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
			it.Detail = "服务器出问题时，只能用仓库或空间站里的备份恢复：会丢掉这 " + agoPlain(age) + "的记忆、会话与配置改动。按 u 把还没同步的逃生舱复制过去（只追加，不改动已有的文件）。"
		}
		return it
	}
	it.Level, it.Key = HealthWarn, "u"
	it.Text = newest + "，但都只在服务器上"
	it.Detail = "服务器坏了或被删光时，只在服务器上的逃生舱会一起丢失。按 u 复制到仓库或空间站；还没有时先在「仓库与空间站」里添加一个（本机文件夹就可以）。"
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
		it.Level = HealthInfo
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
		it.Level, it.Text = HealthInfo, "profile 没有声明服务或管理页"
		return it
	}
	it.Text = strings.Join(parts, " · ")
	return it
}

func approvalItem(d *agentkit.Declaration) HealthItem {
	it := HealthItem{ID: "approval", Group: HealthAgent, Title: "审批"}
	switch {
	case d == nil:
		it.Level, it.Text = HealthInfo, "agent 还没有声明执行风险命令前是否需要你批准"
	case d.ApprovalMode == agentkit.ApprovalBypass:
		it.Level, it.Text = HealthBad, "agent 执行风险命令无需批准（Bypass）（agent 声明，未经核实）"
		it.Detail = "agent 可以不经你同意删除文件、改配置或运行命令；被注入的指令也会直接执行。在 harness 的设置里改为执行前询问，然后让 agent 重新声明。"
	case d.ApprovalMode == agentkit.ApprovalAsk:
		it.Level, it.Text = HealthOK, "执行风险命令前需要你批准（agent 声明，未经核实）"
	default:
		it.Level, it.Text = HealthInfo, "agent 说不清执行风险命令前是否需要你批准"
	}
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
