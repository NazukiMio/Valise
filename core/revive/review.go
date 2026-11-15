package revive

// ReviewCapability binds recovery branches to a fresh, idle journal review.
const ReviewCapability = "restore-review/1"
const MethodReview = "revive_review"
const MethodResumeChecked = "revive_resume_checked"

type ReviewResult struct {
	Journal  Journal  `json:"journal"`
	Digest   string   `json:"digest"`
	Kept     []string `json:"kept,omitempty"`
	Complete bool     `json:"complete"`
	Actions  []string `json:"actions"`
}
type CheckedResumeParams struct {
	OpID   string `json:"op_id"`
	Action string `json:"action"`
	Digest string `json:"digest"`
}
