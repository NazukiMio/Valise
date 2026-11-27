package protection

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/valise"
	"valise/core/vault"
)

var dailyRE = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)
var idRE = regexp.MustCompile(`^[a-f0-9]{32}$`)

var canonicalWeekdays = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

var weekdayOrder = map[string]int{
	"Mon": 1, "Tue": 2, "Wed": 3, "Thu": 4, "Fri": 5, "Sat": 6, "Sun": 7,
}

func validEveryHours(h int) bool {
	switch h {
	case 1, 2, 4, 6, 8, 12:
		return true
	default:
		return false
	}
}

func validWeekdays(days []string) bool {
	if len(days) == 0 {
		return false
	}
	seen := make(map[string]bool, len(days))
	for _, d := range days {
		if weekdayOrder[d] == 0 || seen[d] {
			return false
		}
		seen[d] = true
	}
	return true
}

func sortedWeekdays(days []string) []string {
	out := make([]string, 0, len(days))
	for _, w := range canonicalWeekdays {
		for _, d := range days {
			if d == w {
				out = append(out, w)
				break
			}
		}
	}
	return out
}

func ValidID(s string) bool { return idRE.MatchString(s) }
func ValidateAgent(a identity.AgentRef) error {
	if a.ServerUID == "" || enroll.ValidInstanceID(a.InstallID) != nil || a.Generation == "" || enroll.ValidInstanceID(a.InstanceID) != nil {
		return valise.New("identity_unbound", "实例身份不完整", "重新接入实例后再准备计划")
	}
	return nil
}
func ValidTimezone(s string) bool {
	if s == "" || s == "Local" || strings.ContainsAny(s, " \t\r\n%\\") || strings.HasPrefix(s, "/") || strings.Contains(s, "..") {
		return false
	}
	_, err := time.LoadLocation(s)
	return err == nil
}

func (s Schedule) validate() error {
	if s.Enabled == nil {
		return valise.New("schedule_invalid", "必须明确选择启用状态、每日时间与 IANA 时区", "填写 HH:mm 时间及有效时区")
	}
	cadence := s.EffectiveCadence()
	switch cadence {
	case "daily", "hourly", "weekly", "monthly":
	default:
		return valise.New("schedule_invalid", "定时频率无效", "选择 daily、hourly、weekly 或 monthly")
	}
	switch s.FullCycle {
	case "", FullCycleNone, FullCycleWeekly, FullCycleMonthly:
	default:
		return valise.New("schedule_invalid", "完整快照周期无效", "选择 none、weekly 或 monthly")
	}
	if !*s.Enabled {
		if s.DailyAt != "" && !dailyRE.MatchString(s.DailyAt) {
			return valise.New("schedule_invalid", "必须明确选择启用状态、每日时间与 IANA 时区", "填写 HH:mm 时间及有效时区")
		}
		if s.Timezone != "" && !ValidTimezone(s.Timezone) {
			return valise.New("schedule_invalid", "必须明确选择启用状态、每日时间与 IANA 时区", "填写 HH:mm 时间及有效时区")
		}
		if s.EveryHours != 0 && !validEveryHours(s.EveryHours) {
			return valise.New("schedule_invalid", "按小时定时设置无效", "选择 1、2、4、6、8 或 12 小时间隔")
		}
		if len(s.Weekdays) > 0 && !validWeekdays(s.Weekdays) {
			return valise.New("schedule_invalid", "按周定时设置无效", "选择不重复的有效星期（Mon–Sun）")
		}
		if s.DayOfMonth != 0 && (s.DayOfMonth < 1 || s.DayOfMonth > 28) {
			return valise.New("schedule_invalid", "按月定时设置无效", "选择 1–28 日")
		}
		return nil
	}
	if !ValidTimezone(s.Timezone) {
		return valise.New("schedule_invalid", "必须明确选择启用状态、每日时间与 IANA 时区", "填写 HH:mm 时间及有效时区")
	}
	switch cadence {
	case "daily":
		if !dailyRE.MatchString(s.DailyAt) || s.EveryHours != 0 || len(s.Weekdays) != 0 || s.DayOfMonth != 0 {
			return valise.New("schedule_invalid", "必须明确选择启用状态、每日时间与 IANA 时区", "填写 HH:mm 时间及有效时区")
		}
	case "hourly":
		if !validEveryHours(s.EveryHours) || (s.DailyAt != "" && !dailyRE.MatchString(s.DailyAt)) || len(s.Weekdays) != 0 || s.DayOfMonth != 0 {
			return valise.New("schedule_invalid", "按小时定时设置无效", "选择 1、2、4、6、8 或 12 小时间隔及有效时区")
		}
	case "weekly":
		if !dailyRE.MatchString(s.DailyAt) || !validWeekdays(s.Weekdays) || s.EveryHours != 0 || s.DayOfMonth != 0 {
			return valise.New("schedule_invalid", "按周定时设置无效", "选择星期、HH:mm 时间及有效时区")
		}
	case "monthly":
		if !dailyRE.MatchString(s.DailyAt) || s.DayOfMonth < 1 || s.DayOfMonth > 28 || s.EveryHours != 0 || len(s.Weekdays) != 0 {
			return valise.New("schedule_invalid", "按月定时设置无效", "选择 1–28 日、HH:mm 时间及有效时区")
		}
	}
	return nil
}

func (d Desired) Validate() error {
	if d.Scope.Kind != "preset" {
		return valise.New("unsupported_scope", "仅支持预设备份范围", "选择已有预设范围")
	}
	switch d.Scope.Tier {
	case vault.TierSoul, vault.TierSoulSecrets, vault.TierFull, vault.TierRaw:
	default:
		return valise.New("unsupported_scope", "备份范围无效", "选择已有预设范围")
	}
	if err := d.Schedule.validate(); err != nil {
		return err
	}
	if d.Period.KeepAutoPeriods < 0 {
		return valise.New("period_setting_invalid", "自动快照保留周期数不能为负数", "填 0 关闭退役，或填写正整数")
	}
	if !ValidTimezone(d.Period.Timezone) || (vault.PeriodSetting{Unit: d.Period.Unit, TZ: d.Period.Timezone}).Validate() != nil {
		return valise.New("period_setting_invalid", "周期设置无效", "选择 week、month 或 quarter 及有效时区")
	}
	if d.ServerCopies == nil {
		return valise.New("request_invalid", "缺少服务器副本完整设置", "明确提供 server_copies，空列表表示不配置")
	}
	seen := map[LocationRef]bool{}
	for _, c := range d.ServerCopies {
		interval, err := time.ParseDuration(c.MinimumInterval)
		if c.TargetRef.Kind != "legacy_target" || c.TargetRef.ID == "" || c.TargetRef.AuthorityID == "" || c.Enabled == nil || err != nil || interval < time.Hour || seen[c.TargetRef] {
			return valise.New("copy_setting_invalid", "服务器副本引用或频率无效", "选择已有目标并明确启用状态和至少一小时的间隔")
		}
		seen[c.TargetRef] = true
	}
	return nil
}

// ValidateBundleMembers checks a bundle member list: valid unique instance
// ids, at least two, naming the agent itself.
func ValidateBundleMembers(self string, members []string) error {
	if len(members) == 0 {
		return nil
	}
	seen, hasSelf := map[string]bool{}, false
	for _, m := range members {
		if enroll.ValidInstanceID(m) != nil || seen[m] {
			return valise.New("bundle_invalid", "组合备份成员无效或重复", "选择同一 Harness 的已接入实例")
		}
		seen[m], hasSelf = true, hasSelf || m == self
	}
	if len(members) < 2 || !hasSelf {
		return valise.New("bundle_invalid", "组合备份至少包含本实例和另一个同 Harness 实例", "选择至少两个成员，且包含本实例")
	}
	return nil
}

func (p PrepareParams) Validate() error {
	if err := ValidateAgent(p.AgentRef); err != nil {
		return err
	}
	if err := ValidateBundleMembers(p.AgentRef.InstanceID, p.Desired.BundleMembers); err != nil {
		return err
	}
	return p.Desired.Validate()
}
func (p ServerPlan) Validate() error {
	if p.SchemaVersion != Schema {
		return valise.New("unsupported_schema", "保护计划格式不受支持", "更新到支持此格式的版本")
	}
	if p.Revision == 0 || p.AcceptedAt.IsZero() {
		return valise.New("record_corrupt", "保护计划版本或接受时间无效", "核实服务器保护计划记录")
	}
	return (PrepareParams{AgentRef: p.AgentRef, Desired: p.Desired}).Validate()
}

// Calendar contains only validated fields, so systemd specifiers cannot be injected.
func (s Schedule) Calendar() string {
	switch s.EffectiveCadence() {
	case "hourly":
		mm := "00"
		if len(s.DailyAt) == 5 && dailyRE.MatchString(s.DailyAt) {
			mm = s.DailyAt[3:5]
		}
		return fmt.Sprintf("*-*-* 00/%d:%s:00 %s", s.EveryHours, mm, s.Timezone)
	case "weekly":
		days := sortedWeekdays(s.Weekdays)
		return strings.Join(days, ",") + " *-*-* " + s.DailyAt + ":00 " + s.Timezone
	case "monthly":
		return fmt.Sprintf("*-*-%.2d %s:00 %s", s.DayOfMonth, s.DailyAt, s.Timezone)
	default:
		if s.DailyAt == "" || s.Timezone == "" {
			return ""
		}
		return "*-*-* " + s.DailyAt + ":00 " + s.Timezone
	}
}
