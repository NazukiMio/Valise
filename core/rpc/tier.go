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
	// own key): developer mode.
	TierAdmin Tier = "admin"
	// TierOwner is a method class, not a connection: a daily session runs
	// it after proving the Valise passphrase (owner_challenge, owner_prove).
	TierOwner Tier = "owner"
)

// Owner proof methods (docs/ARCHITECTURE.md 2.2).
const (
	MethodOwnerChallenge = "owner_challenge"
	MethodOwnerProve     = "owner_prove"
)

// OwnerChallengeResult is a random nonce encrypted with age to the
// server's recipients.
type OwnerChallengeResult struct {
	Ciphertext []byte `json:"ciphertext"`
}

// OwnerProveParams returns the decrypted nonce.
type OwnerProveParams struct {
	Nonce []byte `json:"nonce"`
}

// dailyMethods are allowed on the daily tier: reads, sealing and syncing.
var dailyMethods = map[string]bool{
	MethodHello:               true,
	"detect":                  true,
	"enroll_scan":             true,
	"instance_get":            true,
	"instance_list":           true,
	"vault_status":            true,
	"vault_verify":            true,
	"vault_files":             true,
	"seal":                    true,
	"sync":                    true,
	"target_list":             true,
	"seal_log":                true,
	"timer_status":            true,
	"identity_get":            true,
	"protection_get":          true,
	"protection_prepare":      true,
	"protection_status":       true,
	"protection_backup":       true,
	"backup_run_status":       true,
	"backup_snapshot_binding": true,
	"target_change_prepare":   true,
	"system_status":           true,
	"target_check":            true,
	"leaf_materials":          true,
	"target_verify":           true,
	"system_fix_audit":        true,
	"backup_run_cancel":       true,
	"checkup":                 true,
	"proposals_list":          true,
	"kit_status":              true,
	"kit_tools_check":         true,
	"approval_check":          true,
	"action_plan":             true,
	"action_status":           true,
	"prev_list":               true,
	"old_list":                true,
	"journal_get":             true,
	"listen_addrs":            true,
	// Appending an audit entry after an admin operation must not need the
	// admin window again; it changes nothing protected.
	"install_audit":     true,
	"terminal_sessions": true,
	// Naming is allowed once; porter refuses to rename.
	"server_name_set":    true,
	MethodOwnerChallenge: true,
	MethodOwnerProve:     true,
}

// conditionalMethods reach their handler on the daily tier, which picks the
// tier from the request and server state (session.require): recipient_add
// (the first recipient is daily, more need the owner), action_run (by the
// action's risk, docs/INSTALL.md section 5) and the deletions (owner when
// what is deleted can still be found elsewhere, developer mode otherwise).
var conditionalMethods = map[string]bool{
	"recipient_add":      true,
	"action_run":         true,
	"action_resume":      true,
	"action_cleanup":     true,
	"leaf_delete":        true,
	"target_leaf_delete": true,
	"prev_drop":          true,
}

// ownerMethods are basic operations that write into the agent or change
// where backups go: the daily key plus the owner proof.
var ownerMethods = map[string]bool{
	"instance_write":       true,
	"server_rename":        true,
	"agent_label_set":      true,
	"target_set":           true,
	"period_set":           true,
	"timer_set":            true,
	"protection_apply":     true,
	"protection_reconcile": true,
	"target_change_apply":  true,
	"proposals_decide":     true,
	"kit_install":          true,
	"kit_uninstall":        true,
	"login_prepare":        true,
	"login_urls":           true,
	"login_end":            true,
	"entry_auth":           true,
	"revive_inspect":       true,
	"stage_begin":          true,
	"stage_verify":         true,
	"swap":                 true,
	"revive_resume":        true,
	"service_control":      true,
	"station_key":          true,
	"sync_record":          true,
	"station_cert":         true,
	"station_probe":        true,
	"kit_tool_begin":       true,
	"kit_tool_remove":      true,
	"cloud_login_prepare":  true,
	"cloud_forget":         true,
}

// adminMethods carry security risks: developer mode. Listed explicitly so
// a new method has to be classified.
var adminMethods = map[string]bool{
	"entry_port_set":   true,
	"recipient_remove": true,
	"old_cleanup":      true,
	"install_check":    true,
	// Removing the server from Valise: stops timers, marks server.json and
	// deletes porter (docs/ARCHITECTURE.md 2.3).
	"server_leave": true,
	// Ends a process: only one Valise started, after the user confirms.
	"terminal_session_end": true,
}

// TierOf returns the tier a method needs. Conditional methods report
// TierDaily; their handler refuses when the condition does not hold.
// Unclassified methods need TierAdmin.
func TierOf(method string) Tier {
	switch {
	case dailyMethods[method] || conditionalMethods[method]:
		return TierDaily
	case ownerMethods[method]:
		return TierOwner
	}
	return TierAdmin
}

// Conditional reports whether a daily call may still be refused with
// AdminRequired depending on server state.
func Conditional(method string) bool { return conditionalMethods[method] }

// Classified reports whether a method is listed in one of the tiers.
func Classified(method string) bool {
	return dailyMethods[method] || conditionalMethods[method] || ownerMethods[method] || adminMethods[method]
}

// AdminRequiredCode is the UserError code for a method refused on the daily
// tier.
const AdminRequiredCode = "admin_required"

// OwnerRequiredCode is the UserError code for an owner method on a daily
// session that has not proved the Valise passphrase.
const OwnerRequiredCode = "owner_required"

// OwnerRequired is the error for an owner method without the proof.
func OwnerRequired(method string) *valise.UserError {
	return valise.New(OwnerRequiredCode, "这一步需要确认是你本人（"+method+"）",
		"输入 Valise 口令后重试；Valise 会在需要时询问")
}

// AdminRequired is the error a daily porter returns for an admin method.
func AdminRequired(method string) *valise.UserError {
	return valise.New(AdminRequiredCode, "这一步需要开发者模式（"+method+"）",
		"用管理钥匙或服务器密码登录后重试；Valise 会在需要时询问")
}
