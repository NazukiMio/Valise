package gui

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
	"valise/core/revive"
	"valise/core/valise"
)

func TestAgentRenameHidesWhenRetired(t *testing.T) {
	a, tt := world(t)
	a.agents.value.Items[0].Instance.Superseded = &enroll.Supersession{
		By: "other", OpID: "op_1", At: time.Now(),
	}
	a.router.Push("/agents/home/xiaoman")
	tt.Frame()
	if tt.HasText(T("agent.rename_open")) {
		t.Fatalf("retired agent must not offer rename: %q", tt.Texts())
	}
}

func TestServerCleanupCard(t *testing.T) {
	a, _ := settingsWorld(t)
	tt := ui.NewTester(a.View, 1180, 1800)
	recent := time.Now().Add(-2 * time.Hour)
	older := time.Now().Add(-10 * 24 * time.Hour)
	prevData := []revive.PrevEntry{
		{OpID: "op_1", Path: "/home/me/.valise-prev-op_1-state", Created: recent, Bytes: 4096},
		{OpID: "op_old", Path: "/home/me/.valise-prev-op_old-state", Created: older, Bytes: 2048},
	}
	oldData := []revive.OldEntry{
		{
			InstanceID: "openclaw-old",
			Superseded: enroll.Supersession{By: "openclaw-new", OpID: "op_mig", At: recent},
			Leftovers:  []revive.PrevEntry{{OpID: "op_mig", Path: "/home/me/.openclaw-prev", Created: recent, Bytes: 8192}},
		},
	}
	var droppedOps, cleanedIDs []string
	var oldListErr error
	a.cleanupHook = &cleanupOps{
		prevList: func(context.Context, string) (*revive.PrevListResult, error) {
			return &revive.PrevListResult{Entries: append([]revive.PrevEntry{}, prevData...)}, nil
		},
		prevDrop: func(_ context.Context, _ string, op string) ([]string, error) {
			droppedOps = append(droppedOps, op)
			var keep []revive.PrevEntry
			for _, e := range prevData {
				if e.OpID != op {
					keep = append(keep, e)
				}
			}
			prevData = keep
			return []string{"/home/me/.valise-prev-" + op + "-state"}, nil
		},
		oldList: func(context.Context, string) (*revive.OldListResult, error) {
			if oldListErr != nil {
				return nil, oldListErr
			}
			return &revive.OldListResult{Entries: append([]revive.OldEntry{}, oldData...)}, nil
		},
		oldCleanup: func(_ context.Context, _ string, inst string) ([]string, error) {
			cleanedIDs = append(cleanedIDs, inst)
			oldData = nil
			return []string{"/home/me/.openclaw-prev"}, nil
		},
	}

	a.serverTab = 1
	a.router.Push("/servers/home")
	tt.Frame()
	click(t, tt, T("cleanup.refresh"))
	hasAll(t, tt.Texts(),
		T("cleanup.title"),
		"/home/me/.valise-prev-op_1-state",
		"/home/me/.valise-prev-op_old-state",
		"openclaw-old",
		T("need.valise"),
		T("need.admin"),
	)

	// Cancelling prev drop invokes nothing.
	click(t, tt, T("cleanup.drop_open"))
	hasAll(t, tt.Texts(), T("cleanup.drop_title"), T("cleanup.drop_warn"))
	click(t, tt, T("common.cancel"))
	if len(droppedOps) != 0 {
		t.Fatalf("cancelled drop still ran: %v", droppedOps)
	}

	// Confirming first prev drop deletes it and refreshes the list.
	click(t, tt, T("cleanup.drop_open"))
	click(t, tt, T("cleanup.drop_go"))
	tt.Advance(time.Second)
	if len(droppedOps) != 1 || droppedOps[0] != "op_1" {
		t.Fatalf("droppedOps = %v", droppedOps)
	}
	if tt.HasText("/home/me/.valise-prev-op_1-state") || !tt.HasText(T("cleanup.dropped", "n", 1)) {
		t.Fatalf("unexpected texts after prev drop: %q", tt.Texts())
	}

	// Confirming old cleanup deletes old instance and refreshes list.
	click(t, tt, T("cleanup.old_open"))
	hasAll(t, tt.Texts(), T("cleanup.old_title_ask", "id", "openclaw-old"), T("cleanup.old_warn"))
	click(t, tt, T("cleanup.old_go"))
	tt.Advance(time.Second)
	if len(cleanedIDs) != 1 || cleanedIDs[0] != "openclaw-old" {
		t.Fatalf("cleanedIDs = %v", cleanedIDs)
	}
	if tt.HasText("openclaw-old") && !tt.HasText(T("cleanup.old_cleaned", "id", "openclaw-old", "n", 1)) {
		t.Fatalf("expected cleanup note: %q", tt.Texts())
	}

	// Drop remaining prev item -> shows empty message.
	click(t, tt, T("cleanup.drop_open"))
	click(t, tt, T("cleanup.drop_go"))
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("cleanup.empty"))

	// Partial error: oldList fails while prevList succeeds.
	oldListErr = valise.New("old_list_failed", "old_list failed", "")
	click(t, tt, T("cleanup.refresh"))
	if !tt.HasText("old_list failed") {
		t.Fatalf("partial read error not shown: %q", tt.Texts())
	}
}
