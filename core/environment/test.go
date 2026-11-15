package environment

import (
	"time"

	"valise/core/identity"
	"valise/core/vault"
)

const TestCapability = "environment-test/1"
const TestCompositionCapability = "environment-test/2"
const MethodTest = "environment_test"
const PlanCapability = "environment-restore-plan/1"
const MethodPlan = "environment_restore_plan"

// TestInput is frozen with a test-only stage by the human client after
// decrypting and checking the selected copy. It conveys no execution evidence.
type TestInput struct {
	Baseline     *TestSnapshot  `json:"baseline,omitempty"`
	VaultID      string         `json:"vault_id"`
	HeaderDigest string         `json:"header_digest"`
	Header       vault.Snapshot `json:"header"`
	Files        vault.Files    `json:"files"`
}

// TestSnapshot is a separately authenticated data source, never live files.
type TestSnapshot struct {
	VaultID      string         `json:"vault_id"`
	HeaderDigest string         `json:"header_digest"`
	Header       vault.Snapshot `json:"header"`
	Files        vault.Files    `json:"files"`
}

type DataOrigin struct {
	Snapshot identity.SnapshotRef `json:"snapshot"`
	Time     time.Time            `json:"time"`
}

// ProbeResult contains bounded public readiness facts; never program output or
// HTTP/offline response bodies. It is not formal activation or human acceptance.
type ProbeResult struct {
	ProofKind        string `json:"proof_kind,omitempty"`
	ProcessStarted   bool   `json:"process_started"`
	PortListening    bool   `json:"port_listening"`
	HTTPResponded    bool   `json:"http_responded"`
	HTTPStatus       int    `json:"http_status,omitempty"`
	SocketListening  bool   `json:"socket_listening"`
	OfflineResponded bool   `json:"offline_responded"`
	GatewayPassed    bool   `json:"gateway_passed"`
	ProcessesStopped bool   `json:"processes_stopped"`
	Completed        bool   `json:"completed"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	FailureCode      string `json:"failure_code,omitempty"`
}

func (r ProbeResult) Verified() bool {
	proof := (r.ProofKind == "" || r.ProofKind == "http") && r.PortListening && r.HTTPResponded ||
		r.ProofKind == "unix-json" && r.SocketListening && r.OfflineResponded
	return proof && r.Completed && r.ProcessStarted && r.GatewayPassed && r.ProcessesStopped && r.FailureCode == ""
}

// TestResult is durable before any program starts. An interrupted probing
// phase is unknown; querying it never runs the test again.
type TestResult struct {
	Baseline       *DataOrigin `json:"baseline,omitempty"`
	ProbeAttempted bool        `json:"probe_attempted"`
	State          string      `json:"state"`
	CandidateBuilt bool        `json:"candidate_built"`
	ProgramSHA256  string      `json:"program_sha256,omitempty"`
	DataPlanDigest string      `json:"data_plan_digest,omitempty"`
	Probe          ProbeResult `json:"probe"`
	FailureCode    string      `json:"failure_code,omitempty"`
}
