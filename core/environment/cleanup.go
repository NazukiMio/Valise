package environment

import "valise/core/identity"

const (
	MethodOldCleanupReview  = "environment_restore_old_cleanup_review"
	MethodOldCleanup        = "environment_restore_old_cleanup"
	MethodCurrentOldCleanup = "environment_restore_old_cleanup_current"
)

type OldCleanupItem struct {
	Kind    string `json:"kind"`
	Root    string `json:"root,omitempty"`
	Present bool   `json:"present"`
	Files   int64  `json:"files"`
	Bytes   int64  `json:"bytes"`
}

type OldCleanupReview struct {
	PreparationParams
	Snapshot   identity.SnapshotRef `json:"snapshot"`
	Mode       Mode                 `json:"mode"`
	PolicyMode Mode                 `json:"policy_mode"`
	Target     Ref                  `json:"target"`
	Current    Ref                  `json:"current"`
	State      string               `json:"state"`
	Items      []OldCleanupItem     `json:"items"`
	Digest     string               `json:"digest"`
}
