// Package action holds the RPC types of managed actions (docs/INSTALL.md
// section 5).
package action

// MethodRun runs one action declared in an instance's stored profile.
const MethodRun = "action_run"

// RunParams names the action; the porter looks the command up in the
// profile stored at enrollment, never taking one from the client.
type RunParams struct {
	InstanceID string `json:"instance_id"`
	ActionID   string `json:"action_id"`
	// Sealed records in the audit that a backup was taken first.
	Sealed     bool   `json:"sealed,omitempty"`
	PlanDigest string `json:"plan_digest,omitempty"`
	BackupID   string `json:"backup_id,omitempty"`
	// LinkHandling is the staged link handling version the client shows
	// in its confirmation (INSTALL.md 5.2.1). A plan with links runs only
	// when it is at least LinkHandlingVersion; an older client is refused
	// before any side effect.
	LinkHandling int `json:"link_handling,omitempty"`
}

// LinkHandlingVersion is the current staged link handling.
const LinkHandlingVersion = 1

// RunResult is the outcome. Output is the tail of a text action's output;
// Secret is set only for secret actions and is never logged.
type RunResult struct {
	Exit      int        `json:"exit"`
	Output    string     `json:"output,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
	Secret    string     `json:"secret,omitempty"`
	Operation *Operation `json:"operation,omitempty"`
}

// MaxOutputLines is how much of a text action's output is kept.
const MaxOutputLines = 200

// MethodApproval runs the stored profile's approval_check (read only).
const MethodApproval = "approval_check"

// ApprovalParams names the instance.
type ApprovalParams struct {
	InstanceID string `json:"instance_id"`
}

// ApprovalResult is what the harness's own settings say. Declared is false
// when the profile has no approval_check; Pending when the check is not
// verified on a target yet.
type ApprovalResult struct {
	Declared bool   `json:"declared"`
	Mode     string `json:"mode,omitempty"` // profile.Approval*
	Detail   string `json:"detail,omitempty"`
	Pending  bool   `json:"pending,omitempty"`
	// Default: the bypass is the harness's factory setting (default_when).
	Default bool `json:"default,omitempty"`
}
