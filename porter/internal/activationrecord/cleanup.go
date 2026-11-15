package activationrecord

import (
	"path/filepath"
	"strings"

	"valise/core/environment"
	"valise/core/revive"
)

// Cleanup is private executor progress. Intent is written before deletion;
// neither it nor a public review can restore a human's deletion consent.
type Cleanup struct {
	Target       environment.Ref `json:"target"`
	Items        []CleanupItem   `json:"items"`
	Complete     bool            `json:"complete"`
	ProbeStopped bool            `json:"probe_stopped"`
	IssueCode    string          `json:"issue_code,omitempty"`
}

type CleanupItem struct {
	Kind     string             `json:"kind"`
	Root     string             `json:"root,omitempty"`
	Identity revive.ManagedRoot `json:"identity"`
	Intent   bool               `json:"intent"`
	Removed  bool               `json:"removed"`
}

func validCleanup(r *Record) bool {
	c := r.Cleanup
	if c == nil {
		return true
	}
	if c.Target != r.Plan.Original || !c.ProbeStopped || len(c.Items) < 3 || len(c.Items) > len(r.Plan.OriginalProfile.Roots)+3 || (c.IssueCode != "" && c.IssueCode != "environment_cleanup_interrupted" && c.IssueCode != "environment_cleanup_io_failed") {
		return false
	}
	seen, programs, temporary := map[string]bool{}, 0, 0
	for _, item := range c.Items {
		key := item.Kind + ":" + item.Root
		if seen[key] || item.Identity.ID != key || item.Identity.Inode == 0 || item.Removed && !item.Intent || c.Complete && !item.Removed {
			return false
		}
		seen[key] = true
		switch item.Kind {
		case "program":
			if item.Root != "" {
				return false
			}
			programs++
		case "preparation":
			if item.Root != "" {
				return false
			}
			temporary++
		case "data":
			if item.Root == "" || item.Root == "." || item.Root == ".." || item.Root != filepath.Base(item.Root) || strings.ContainsAny(item.Root, "/\\\x00") {
				return false
			}
		default:
			return false
		}
	}
	return programs == 1 && temporary == 1 && (!c.Complete || c.IssueCode == "")
}
