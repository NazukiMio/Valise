package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"valise/core/identity"
	"valise/core/protection"
)

// This tests the local controller at its typed RPC boundary, without running a
// remote command. The shared Service/porter wire integration uses fake-ssh.
type protectionBoundary struct {
	mu            sync.Mutex
	now           time.Time
	prepareCount  int
	applyCount    int
	statusCount   int
	preconditions string
	applyError    error
	statusError   error
	applyHook     func(protection.ApplyParams)
	change        protection.Change
}

func (b *protectionBoundary) call(_ context.Context, _ string, method string, params, result any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch method {
	case protection.MethodPrepare:
		b.prepareCount++
		req := params.(protection.PrepareParams)
		id, _ := randomJourneyID()
		plan := protection.FrozenChange{PlanID: id, Kind: "protection_change", CreatedAt: b.now, ExpiresAt: b.now.Add(10 * time.Minute), Request: req, PreconditionsDigest: b.preconditions, CalendarPreview: "daily"}
		plan.PlanDigest, _ = plan.Digest()
		*result.(*protection.FrozenChange) = plan
	case protection.MethodApply:
		b.applyCount++
		req := params.(protection.ApplyParams)
		if b.applyHook != nil {
			b.applyHook(req)
		}
		digest, _ := protection.Digest(req.Plan.Request)
		b.change = protection.Change{Schema: "valise-protection-change/1", ChangeID: req.ChangeID, AgentRef: req.Plan.Request.AgentRef, RequestDigest: digest, State: "committed", OldRevision: req.Plan.Request.ExpectedRevision, NewRevision: req.Plan.Request.ExpectedRevision + 1, Committed: true, UpdatedAt: b.now}
		*result.(*protection.Change) = b.change
		return b.applyError
	case protection.MethodStatus:
		b.statusCount++
		*result.(*protection.Change) = b.change
		return b.statusError
	case protection.MethodGet:
		*result.(*protection.View) = protection.View{Mode: "legacy", Timer: protection.TimerObservation{Status: "unknown"}}
	default:
		panic("unexpected method")
	}
	return nil
}
func protectionRequest() protection.PrepareParams {
	desired := protection.NewDefault()
	desired.Schedule.DailyAt = "12:30"
	desired.Schedule.Timezone = "UTC"
	desired.Period = protection.Period{Unit: "month", Timezone: "UTC"}
	return protection.PrepareParams{AgentRef: identity.AgentRef{ServerUID: "server", InstallID: "sample", InstanceID: "sample", Generation: "generation"}, Desired: desired}
}
func protectionController(t *testing.T, b *protectionBoundary, review ProtectionReview) *ProtectionController {
	t.Helper()
	c, err := NewProtectionController(ProtectionControllerConfig{StateDir: t.TempDir(), RPC: b.call, Review: review})
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return b.now }
	return c
}
func prepareProtection(t *testing.T, c *ProtectionController) protection.FrozenChange {
	t.Helper()
	plan, err := c.PrepareProtectionChange(context.Background(), "host", protectionRequest())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestProtectionWaitingConfirmDedupAndPersistence(t *testing.T) {
	b := &protectionBoundary{now: time.Now().UTC(), preconditions: "facts"}
	c := protectionController(t, b, nil)
	plan := prepareProtection(t, c)
	task, err := c.StartProtectionChange(context.Background(), plan.PlanID, "intent")
	if err != nil || task.State != "waiting" || task.Wait != "confirmation" || b.applyCount != 0 {
		t.Fatalf("waiting: %+v %v", task, err)
	}
	// The disk has no confirmation authority, even if someone adds approved.
	p := filepath.Join(c.taskDir(), "protection-"+task.TaskRef.TaskID+".json")
	raw, _ := os.ReadFile(p)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	fields["approved"] = true
	raw, _ = json.Marshal(fields)
	if err = os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	reviewCount := 0
	c.config.Review = func(_ context.Context, _ string, review protection.FrozenChange) (bool, error) {
		reviewCount++
		review.Request.Desired.Scope.Tier = "raw"
		return true, nil
	}
	b.applyHook = func(req protection.ApplyParams) {
		if req.Plan.Request.Desired.Scope.Tier != "full" {
			t.Error("review mutated frozen choices")
		}
		data, e := os.ReadFile(p)
		var persisted ProtectionTask
		if e != nil || json.Unmarshal(data, &persisted) != nil || persisted.State != "running" {
			t.Error("applied before durable running record")
		}
	}
	task, err = c.StartProtectionChange(context.Background(), plan.PlanID, "intent")
	if err != nil || task.State != "succeeded" || reviewCount != 1 || b.applyCount != 1 {
		t.Fatalf("start: %+v %v", task, err)
	}
	again, err := c.StartProtectionChange(context.Background(), plan.PlanID, "intent")
	if err != nil || again.TaskRef != task.TaskRef || b.applyCount != 1 || reviewCount != 1 {
		t.Fatalf("retry: %+v %v", again, err)
	}
	// Reopening does not need the old in-memory plan to query known results.
	reopened, err := NewProtectionController(ProtectionControllerConfig{StateDir: c.config.StateDir, RPC: b.call})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := reopened.GetTask(context.Background(), task.TaskRef)
	if err != nil || observed.State != "succeeded" {
		t.Fatalf("reopen: %+v %v", observed, err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Fatalf("task permissions %v", st.Mode())
	}
	st, _ = os.Stat(c.taskDir())
	if st.Mode().Perm() != 0700 {
		t.Fatalf("directory permissions %v", st.Mode())
	}
}

func TestProtectionSubmissionConflictStaleAndExpiry(t *testing.T) {
	b := &protectionBoundary{now: time.Now().UTC(), preconditions: "facts"}
	c := protectionController(t, b, nil)
	plan := prepareProtection(t, c)
	_, err := c.StartProtectionChange(context.Background(), plan.PlanID, "intent")
	if err != nil {
		t.Fatal(err)
	}
	req := protectionRequest()
	req.Desired.Scope.Tier = "soul"
	other, err := c.PrepareProtectionChange(context.Background(), "host", req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.StartProtectionChange(context.Background(), other.PlanID, "intent")
	if journeyCode(err) != "request_conflict" {
		t.Fatalf("key conflict: %v", err)
	}
	c.config.Review = func(context.Context, string, protection.FrozenChange) (bool, error) {
		b.preconditions = "changed"
		return true, nil
	}
	_, err = c.StartProtectionChange(context.Background(), plan.PlanID, "intent")
	if journeyCode(err) != "plan_stale" || b.applyCount != 0 {
		t.Fatalf("stale: %v count %d", err, b.applyCount)
	}
	c.now = func() time.Time { return b.now.Add(11 * time.Minute) }
	_, err = c.StartProtectionChange(context.Background(), other.PlanID, "new-intent")
	if journeyCode(err) != "plan_expired" || b.applyCount != 0 {
		t.Fatalf("expired: %v", err)
	}
}

func TestProtectionUnknownReconcilesWithoutResend(t *testing.T) {
	b := &protectionBoundary{now: time.Now().UTC(), preconditions: "facts", applyError: errors.New("private transport details must not escape")}
	c := protectionController(t, b, func(context.Context, string, protection.FrozenChange) (bool, error) { return true, nil })
	plan := prepareProtection(t, c)
	task, err := c.StartProtectionChange(context.Background(), plan.PlanID, "intent")
	if err != nil || task.State != "reconciling" || b.applyCount != 1 {
		t.Fatalf("unknown: %+v %v", task, err)
	}
	reopened, err := NewProtectionController(ProtectionControllerConfig{StateDir: c.config.StateDir, RPC: b.call})
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = c.now
	// A fresh plan and the same intent retrieve the original task, never replay.
	newPlan := prepareProtection(t, reopened)
	same, err := reopened.StartProtectionChange(context.Background(), newPlan.PlanID, "intent")
	if err != nil || same.TaskRef != task.TaskRef || b.applyCount != 1 {
		t.Fatalf("reopen dedup: %+v %v", same, err)
	}
	b.statusError = errors.New("status absent")
	observed, err := reopened.GetTask(context.Background(), task.TaskRef)
	if err != nil || observed.State != "reconciling" || b.applyCount != 1 {
		t.Fatalf("missing status: %+v %v", observed, err)
	}
	b.statusError = nil
	observed, err = reopened.GetTask(context.Background(), task.TaskRef)
	if err != nil || observed.State != "succeeded" || b.applyCount != 1 || b.statusCount != 2 {
		t.Fatalf("reconciled: %+v %v", observed, err)
	}
}

func TestProtectionRefusalAndCorruptIndex(t *testing.T) {
	b := &protectionBoundary{now: time.Now().UTC(), preconditions: "facts"}
	c := protectionController(t, b, func(context.Context, string, protection.FrozenChange) (bool, error) { return false, nil })
	plan := prepareProtection(t, c)
	task, err := c.StartProtectionChange(context.Background(), plan.PlanID, "refuse")
	if err != nil || task.State != "cancelled" || b.applyCount != 0 {
		t.Fatalf("refused: %+v %v", task, err)
	}
	if err = os.WriteFile(filepath.Join(c.taskDir(), "protection-"+task.TaskRef.TaskID+".json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = c.StartProtectionChange(context.Background(), plan.PlanID, "new")
	if journeyCode(err) != "record_corrupt" || b.applyCount != 0 {
		t.Fatalf("corruption: %v", err)
	}
}

func TestProtectionWaitingRestartRequiresNewReview(t *testing.T) {
	b := &protectionBoundary{now: time.Now().UTC(), preconditions: "facts"}
	c := protectionController(t, b, nil)
	plan := prepareProtection(t, c)
	waiting, err := c.StartProtectionChange(context.Background(), plan.PlanID, "intent")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewProtectionController(ProtectionControllerConfig{StateDir: c.config.StateDir, RPC: b.call})
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = c.now
	if _, err = reopened.StartProtectionChange(context.Background(), plan.PlanID, "intent"); journeyCode(err) != "plan_stale" {
		t.Fatalf("old plan revived: %v", err)
	}
	fresh := prepareProtection(t, reopened)
	task, err := reopened.StartProtectionChange(context.Background(), fresh.PlanID, "intent")
	if err != nil || task.TaskRef != waiting.TaskRef || task.State != "waiting" || b.applyCount != 0 {
		t.Fatalf("restart consent: %+v %v", task, err)
	}
	reviews := 0
	reopened.config.Review = func(context.Context, string, protection.FrozenChange) (bool, error) { reviews++; return true, nil }
	task, err = reopened.StartProtectionChange(context.Background(), fresh.PlanID, "intent")
	if err != nil || task.State != "succeeded" || reviews != 1 || b.applyCount != 1 {
		t.Fatalf("new consent: %+v %v", task, err)
	}
}

func TestProtectionStatusMustMatchRevisionAndRequest(t *testing.T) {
	b := &protectionBoundary{now: time.Now().UTC(), preconditions: "facts", applyError: errors.New("lost response")}
	c := protectionController(t, b, func(context.Context, string, protection.FrozenChange) (bool, error) { return true, nil })
	plan := prepareProtection(t, c)
	task, err := c.StartProtectionChange(context.Background(), plan.PlanID, "intent")
	if err != nil {
		t.Fatal(err)
	}
	b.change.NewRevision = 42
	task, err = c.GetTask(context.Background(), task.TaskRef)
	if err != nil || task.State != "reconciling" {
		t.Fatalf("wrong revision proved success: %+v %v", task, err)
	}
	b.change.NewRevision = 1
	b.change.RequestDigest = "other-request"
	task, err = c.GetTask(context.Background(), task.TaskRef)
	if err != nil || task.State != "reconciling" {
		t.Fatalf("wrong request proved success: %+v %v", task, err)
	}
	if b.applyCount != 1 {
		t.Fatalf("status replayed apply %d", b.applyCount)
	}
}

func TestProtectionConfirmationCannotAuthorizeTwoTasks(t *testing.T) {
	b := &protectionBoundary{now: time.Now().UTC(), preconditions: "facts"}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	c := protectionController(t, b, func(context.Context, string, protection.FrozenChange) (bool, error) {
		entered <- struct{}{}
		<-release
		return true, nil
	})
	plan := prepareProtection(t, c)
	var wg sync.WaitGroup
	wg.Add(2)
	for _, key := range []string{"one", "two"} {
		go func(k string) { defer wg.Done(); _, _ = c.StartProtectionChange(context.Background(), plan.PlanID, k) }(key)
	}
	<-entered
	<-entered
	close(release)
	wg.Wait()
	if b.applyCount != 1 {
		t.Fatalf("confirmation reused for %d applications", b.applyCount)
	}
}

// TestProtectionServerClockSkew: the plan window is counted on the client
// clock from receipt, so a server clock ahead or behind does not make a
// fresh plan look invalid; the client still expires it ten minutes later.
func TestProtectionServerClockSkew(t *testing.T) {
	for _, skew := range []time.Duration{3 * time.Hour, -2 * 24 * time.Hour} {
		b := &protectionBoundary{now: time.Now().UTC().Add(skew), preconditions: "facts"}
		c := protectionController(t, b, func(context.Context, string, protection.FrozenChange) (bool, error) { return true, nil })
		local := time.Now().UTC()
		c.now = func() time.Time { return local }
		plan := prepareProtection(t, c)
		task, err := c.StartProtectionChange(context.Background(), plan.PlanID, "skew")
		if err != nil || b.applyCount != 1 {
			t.Fatalf("skew %v: %v %+v apply %d", skew, err, task, b.applyCount)
		}
		late := prepareProtection(t, c)
		c.now = func() time.Time { return local.Add(11 * time.Minute) }
		if _, err := c.StartProtectionChange(context.Background(), late.PlanID, "late"); journeyCode(err) != "plan_expired" {
			t.Fatalf("skew %v: late plan: %v", skew, err)
		}
	}
}
