package environment

import "valise/core/identity"

// This capability remains unadvertised until the human acceptance, cleanup
// and subsequent-restore lifecycle is available alongside these methods.
const SwitchCapability = "environment-restore-switch/1"

const (
	MethodSwitchReview = "environment_restore_switch_review"
	MethodSwitchStatus = "environment_restore_switch_status"
	MethodSwitch       = "environment_restore_switch"
	MethodReturn       = "environment_restore_return"
	MethodConfirm      = "environment_restore_confirm"
)

type SwitchParams struct {
	PreparationParams
	Digest        string `json:"digest"`
	AllowUntested bool   `json:"allow_untested"`
}

type ReviewedOperationParams struct {
	PreparationParams
	Digest string `json:"digest"`
}

// SwitchStatus contains separate observations, never a private executable plan.
type SwitchStatus struct {
	PreparationParams
	Snapshot            identity.SnapshotRef `json:"snapshot"`
	Mode                Mode                 `json:"mode"`
	Original            Ref                  `json:"original"`
	Candidate           Ref                  `json:"candidate"`
	Current             *Ref                 `json:"current,omitempty"`
	State               string               `json:"state"`
	Phase               Phase                `json:"phase,omitempty"`
	FailureCode         string               `json:"failure_code,omitempty"`
	IssueCode           string               `json:"issue_code,omitempty"`
	CurrentObserved     bool                 `json:"current_observed"`
	CurrentRunning      bool                 `json:"current_running"`
	CurrentStopped      bool                 `json:"current_stopped"`
	OriginalNative      bool                 `json:"original_native"`
	OriginalCleaned     bool                 `json:"original_cleaned"`
	CleanupState        string               `json:"cleanup_state,omitempty"`
	CleanupIssueCode    string               `json:"cleanup_issue_code,omitempty"`
	CanReviewOldCleanup bool                 `json:"can_review_old_cleanup"`
	OriginalObserved    bool                 `json:"original_observed"`
	OriginalRunning     bool                 `json:"original_running"`
	OriginalStopped     bool                 `json:"original_stopped"`
	GatewayObserved     bool                 `json:"gateway_observed"`
	PortOwned           bool                 `json:"port_owned"`
	HTTPResponded       bool                 `json:"http_responded"`
	GatewayPassed       bool                 `json:"gateway_passed"`
	ConversationKnown   bool                 `json:"conversation_confirmed"`
	Historical          bool                 `json:"historical,omitempty"`
	HistoricalConfirmed bool                 `json:"historical_confirmed,omitempty"`
}

type SwitchReview struct {
	Status                  SwitchStatus `json:"status"`
	Digest                  string       `json:"digest"`
	ConfirmationDigest      string       `json:"confirmation_digest,omitempty"`
	CanSwitch               bool         `json:"can_switch"`
	CanReturn               bool         `json:"can_return"`
	CanConfirm              bool         `json:"can_confirm"`
	ProgramsChecked         bool         `json:"programs_checked"`
	OriginalProgramChecked  bool         `json:"original_program_checked"`
	CandidateProgramChecked bool         `json:"candidate_program_checked"`
	StageChecked            bool         `json:"stage_checked"`
	SourceChecked           bool         `json:"source_checked"`
	Files                   int          `json:"files"`
	Bytes                   int64        `json:"bytes"`
	EnableOnBoot            bool         `json:"enable_on_boot"`
	ProfileUntested         bool         `json:"profile_untested"`
}
