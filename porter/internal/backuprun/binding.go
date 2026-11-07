package backuprun

import (
	"context"
	"errors"
	"os"
	"strings"

	cp "valise/core/protection"
)

// SnapshotBinding reads only explicit durable backup bindings. It never infers a
// generation from instance names or requires the old generation to remain live.
func (s Store) SnapshotBinding(ctx context.Context, p cp.SnapshotBindingParams) (*cp.SnapshotBinding, error) {
	if p.SnapshotRef.VaultID == "" || p.SnapshotRef.SnapshotID == "" {
		return nil, failure("request_invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &cp.SnapshotBinding{State: "unknown"}
	st, err := os.Lstat(s.dir())
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, failure("record_corrupt")
	}
	entries, err := os.ReadDir(s.dir())
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !cp.ValidID(id) {
			return nil, failure("record_corrupt")
		}
		run, err := s.load(id)
		if err != nil {
			return nil, err
		}
		if run.Snapshot == nil || *run.Snapshot != p.SnapshotRef {
			continue
		}
		completed := false
		for _, component := range run.Components {
			if component.Kind == "server_backup" && component.State == "succeeded" {
				completed = true
			}
		}
		if !completed {
			continue
		}
		if result.AgentRef != nil && *result.AgentRef != run.AgentRef {
			return nil, failure("identity_conflict")
		}
		a := run.AgentRef
		result.AgentRef = &a
		result.State = "bound"
	}
	return result, nil
}
