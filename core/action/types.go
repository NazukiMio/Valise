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
	// Sealed records in the audit that an escape pod was taken first.
	Sealed bool `json:"sealed,omitempty"`
}

// RunResult is the outcome. Output is the tail of a text action's output;
// Secret is set only for secret actions and is never logged.
type RunResult struct {
	Exit      int    `json:"exit"`
	Output    string `json:"output,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Secret    string `json:"secret,omitempty"`
}

// MaxOutputLines is how much of a text action's output is kept.
const MaxOutputLines = 200
