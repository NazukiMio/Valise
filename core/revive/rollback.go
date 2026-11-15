package revive

// RollbackEvidence reports observed recovery components, independently of the
// original operation's failure. It never contains commands or file contents.
type RollbackEvidence struct {
	Directories string `json:"directories"`
	Program     string `json:"program"`
	Service     string `json:"service"`
	Health      string `json:"health"`
	Problem     string `json:"problem,omitempty"`
}
