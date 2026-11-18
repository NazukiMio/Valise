package protection

import (
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
func (d Desired) Validate() error {
	if d.Scope.Kind != "preset" {
		return valise.New("unsupported_scope", "仅支持预设备份范围", "选择已有预设范围")
	}
	switch d.Scope.Tier {
	case vault.TierSoul, vault.TierSoulSecrets, vault.TierFull, vault.TierRaw:
	default:
		return valise.New("unsupported_scope", "备份范围无效", "选择已有预设范围")
	}
	if d.Schedule.Enabled == nil || !dailyRE.MatchString(d.Schedule.DailyAt) || !ValidTimezone(d.Schedule.Timezone) {
		return valise.New("schedule_invalid", "必须明确选择启用状态、每日时间与 IANA 时区", "填写 HH:mm 时间及有效时区")
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
func (p PrepareParams) Validate() error {
	if err := ValidateAgent(p.AgentRef); err != nil {
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
func (s Schedule) Calendar() string { return "*-*-* " + s.DailyAt + ":00 " + s.Timezone }
