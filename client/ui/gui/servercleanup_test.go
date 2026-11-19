package gui

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
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

func TestServerCleanupBatch(t *testing.T) {
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
	var calls []service.CleanupBatchRequest
	failOp1 := true
	a.cleanupHook = &cleanupOps{
		prevList: func(context.Context, string) (*revive.PrevListResult, error) {
			return &revive.PrevListResult{Entries: append([]revive.PrevEntry{}, prevData...)}, nil
		},
		oldList: func(context.Context, string) (*revive.OldListResult, error) {
			return &revive.OldListResult{Entries: append([]revive.OldEntry{}, oldData...)}, nil
		},
		batch: func(_ context.Context, host string, req service.CleanupBatchRequest) (*service.CleanupBatchResult, error) {
			if host != "home" {
				t.Fatalf("host = %q, want home", host)
			}
			calls = append(calls, req)
			res := &service.CleanupBatchResult{}
			var nextPrev []revive.PrevEntry
			removedOps := map[string]bool{}
			for _, id := range req.OldInstances {
				if id == "openclaw-old" && len(oldData) > 0 {
					res.OldDone++
					res.Removed = append(res.Removed, "/home/me/.openclaw-prev", "实例档案中的 openclaw-old")
					oldData = nil
				}
			}
			for _, op := range req.PrevOps {
				if op == "op_1" && failOp1 {
					res.Failures = append(res.Failures, service.CleanupBatchFailure{
						Kind: "prev", Target: op, Err: valise.New("op_busy", "目录忙，暂时无法删除", "稍后再试"),
					})
					continue
				}
				removedOps[op] = true
				res.PrevDone++
				res.Removed = append(res.Removed, "/home/me/.valise-prev-"+op+"-state")
			}
			for _, e := range prevData {
				if !removedOps[e.OpID] {
					nextPrev = append(nextPrev, e)
				}
			}
			prevData = nextPrev
			return res, nil
		},
	}

	a.serverTab = 1
	a.router.Push("/servers/home")
	tt.Frame()
	click(t, tt, T("cleanup.refresh"))

	// Individual checkbox picks an >=7d prev op -> Valise passphrase tier tag.
	click(t, tt, T("cleanup.pick_prev", "path", "/home/me/.valise-prev-op_old-state"))
	hasAll(t, tt.Texts(), T("cleanup.selected_summary", "n", 1, "size", "2.0 KiB"), T("cleanup.batch_open", "n", 1))

	// Adding an old instance upgrades required tier tag to admin.
	click(t, tt, T("cleanup.pick_old", "id", "openclaw-old"))
	hasAll(t, tt.Texts(), T("cleanup.selected_summary", "n", 2, "size", "10.0 KiB"), T("cleanup.batch_open", "n", 2))

	// Select all picks all 3 items (14.0 KiB) and switches button to clear selection.
	click(t, tt, T("cleanup.select_all"))
	hasAll(t, tt.Texts(), T("cleanup.clear_sel"), T("cleanup.selected_summary", "n", 3, "size", "14.0 KiB"), T("cleanup.batch_open", "n", 3))
	click(t, tt, T("cleanup.clear_sel"))
	hasAll(t, tt.Texts(), T("cleanup.select_all"), T("cleanup.batch_hint"), T("cleanup.batch_open_none"))

	// Re-select all, open modal, cancel -> zero batch calls.
	click(t, tt, T("cleanup.select_all"))
	click(t, tt, T("cleanup.batch_open", "n", 3))
	hasAll(t, tt.Texts(),
		T("cleanup.batch_title", "n", 3),
		T("cleanup.batch_summary", "prev", 2, "old", 1, "size", "14.0 KiB"),
		T("cleanup.batch_warn"),
		T("cleanup.batch_go", "n", 3),
	)
	click(t, tt, T("common.cancel"))
	if len(calls) != 0 {
		t.Fatalf("cancelled batch still executed: %+v", calls)
	}

	// Confirm batch with partial failure: op_old + openclaw-old succeed, op_1 stays selected & shows failure.
	click(t, tt, T("cleanup.batch_open", "n", 3))
	click(t, tt, T("cleanup.batch_go", "n", 3))
	tt.Advance(time.Second)
	if len(calls) != 1 || len(calls[0].PrevOps) != 2 || len(calls[0].OldInstances) != 1 {
		t.Fatalf("unexpected batch call: %+v", calls)
	}
	hasAll(t, tt.Texts(),
		T("cleanup.batch_partial", "done", 2, "removed", 3, "failed", 1),
		T("cleanup.batch_fails_title"),
		T("cleanup.batch_fail_item", "target", "op_1", "reason", "目录忙，暂时无法删除"),
		T("cleanup.batch_open", "n", 1),
	)
	if tt.HasText("/home/me/.valise-prev-op_old-state") || tt.HasText("openclaw-old") {
		t.Fatalf("deleted items still visible: %q", tt.Texts())
	}

	// Retry remaining op_1 and finish completely.
	failOp1 = false
	click(t, tt, T("cleanup.batch_open", "n", 1))
	click(t, tt, T("cleanup.batch_go", "n", 1))
	tt.Advance(time.Second)
	if len(calls) != 2 || len(calls[1].PrevOps) != 1 || calls[1].PrevOps[0] != "op_1" {
		t.Fatalf("second batch call mismatch: %+v", calls)
	}
	hasAll(t, tt.Texts(), T("cleanup.batch_done", "done", 1, "removed", 1), T("cleanup.empty"))
}
