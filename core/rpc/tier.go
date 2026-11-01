package rpc

import "valise/core/valise"

// Tier is the access level of a porter connection (docs/ARCHITECTURE.md
// sections 2.1 and 2.2).
type Tier string

const (
	// TierDaily is the daily key's forced-command porter: no shell, no
	// admin methods.
	TierDaily Tier = "daily"
	// TierAdmin is a full login (admin key, temporary login, or the user's
	// own key).
	TierAdmin Tier = "admin"
)

// dailyMethods are allowed on the daily tier: reads, sealing and syncing.
var dailyMethods = map[string]bool{
	MethodHello:      true,
	"detect":         true,
	"enroll_scan":    true,
	"instance_get":   true,
	"instance_list":  true,
	"vault_status":   true,
	"vault_verify":   true,
	"vault_files":    true,
	"seal":           true,
	"sync":           true,
	"target_list":    true,
	"seal_log":       true,
	"timer_status":   true,
	"checkup":        true,
	"proposals_list": true,
	"kit_status":     true,
	"prev_list":      true,
	"old_list":       true,
	"journal_get":    true,
	"listen_addrs":   true,
}

// conditionalMethods are allowed on the daily tier only in a state the
// handler checks: recipient_add while the server has no recipient yet.
var conditionalMethods = map[string]bool{
	"recipient_add": true,
}

// adminMethods change what is protected, where it goes, or the harness
// itself. Listed explicitly so a new method has to be classified.
var adminMethods = map[string]bool{
	"instance_write":     true,
	"entry_port_set":     true,
	"recipient_remove":   true,
	"target_set":         true,
	"target_leaf_delete": true,
	"leaf_delete":        true,
	"server_name_set":    true,
	"period_set":         true,
	"timer_set":          true,
	"proposals_decide":   true,
	"kit_install":        true,
	"kit_uninstall":      true,
	"login_prepare":      true,
	"login_urls":         true,
	"login_end":          true,
	"entry_auth":         true,
	"revive_inspect":     true,
	"stage_begin":        true,
	"stage_verify":       true,
	"swap":               true,
	"revive_resume":      true,
	"prev_drop":          true,
	"old_cleanup":        true,
	"service_control":    true,
	"install_check":      true,
	"install_audit":      true,
}

// TierOf returns the tier a method needs. Conditional methods report
// TierDaily; their handler refuses when the condition does not hold.
// Unclassified methods need TierAdmin.
func TierOf(method string) Tier {
	if dailyMethods[method] || conditionalMethods[method] {
		return TierDaily
	}
	return TierAdmin
}

// Conditional reports whether a daily call may still be refused with
// AdminRequired depending on server state.
func Conditional(method string) bool { return conditionalMethods[method] }

// Classified reports whether a method is listed in one of the tiers.
func Classified(method string) bool {
	return dailyMethods[method] || conditionalMethods[method] || adminMethods[method]
}

// AdminRequiredCode is the UserError code for a method refused on the daily
// tier.
const AdminRequiredCode = "admin_required"

// AdminRequired is the error a daily porter returns for an admin method.
func AdminRequired(method string) *valise.UserError {
	return valise.New(AdminRequiredCode, "这一步需要管理权限（"+method+"）",
		"用管理钥匙或服务器密码登录后重试；Valise 会在需要时询问")
}
