package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"valise/client/config"
	"valise/core/identity"
	"valise/core/profile"
)

func TestForgetInstanceAndCacheInvalidation(t *testing.T) {
	s, home := enrollWorld(t)
	ctx := context.Background()
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}

	// Populate AgentReader cache and create a local protection binding for the agent.
	list, err := s.ListAgents(ctx, AgentListRequest{ConnectionIDs: []string{"box"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Instance.InstanceID != "demo" || list.Items[0].AgentRef == nil {
		t.Fatalf("expected 1 bound agent, got %+v", list.Items)
	}
	ref := *list.Items[0].AgentRef

	s.Config.Repos = []config.Repo{{Name: "disk", Path: t.TempDir()}}
	if _, err := s.bindingStore().CompareAndSwap(ctx, ref, 0, 1, bindingCopies("disk")); err != nil {
		t.Fatal(err)
	}
	if got, err := s.bindingStore().Read(ctx, ref); err != nil || got == nil {
		t.Fatalf("expected saved binding, got %+v %v", got, err)
	}

	// Forget instance: server metadata & local binding removed, cache invalidated, live dir untouched.
	res, err := s.ForgetInstance(ctx, "box", "demo")
	if err != nil {
		t.Fatalf("ForgetInstance: %v", err)
	}
	if res.InstanceID != "demo" || !slices.Contains(res.Removed, "dossier:demo") {
		t.Fatalf("unexpected forget result: %+v", res)
	}
	if got, err := s.bindingStore().Read(ctx, ref); err != nil || got != nil {
		t.Fatalf("expected binding deleted, got %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".demo", "demo.json")); err != nil {
		t.Fatalf("live state dir must not be deleted: %v", err)
	}

	// Cache for "box" must be empty before the next read.
	s.agentReader().mu.Lock()
	cacheLen := len(s.agentReader().cache)
	s.agentReader().mu.Unlock()
	if cacheLen != 0 {
		t.Fatalf("expected agent cache invalidated, still has %d entries", cacheLen)
	}

	after, err := s.ListAgents(ctx, AgentListRequest{ConnectionIDs: []string{"box"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != 0 {
		t.Fatalf("expected 0 agents after forget, got %+v", after.Items)
	}
}

func TestRemoveServerInvalidatesAgentCache(t *testing.T) {
	s, _ := enrollWorld(t)
	ctx := context.Background()
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListAgents(ctx, AgentListRequest{ConnectionIDs: []string{"box"}}); err != nil {
		t.Fatal(err)
	}
	r := s.agentReader()
	r.mu.Lock()
	r.cache["other:hash"] = AgentServerObservation{ConnectionID: "other", Status: "fresh"}
	before := len(r.cache)
	r.mu.Unlock()
	if before < 2 {
		t.Fatalf("expected >=2 cached entries, got %d", before)
	}
	if err := s.RemoveServerHere("box", nil); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) != 1 || r.cache["other:hash"].ConnectionID != "other" {
		t.Fatalf("expected only other host cached, got %+v", r.cache)
	}
}

func TestMissingStateRootReportedByInstances(t *testing.T) {
	s, home := enrollWorld(t)
	ctx := context.Background()
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, ".demo")); err != nil {
		t.Fatal(err)
	}
	insts, err := s.Instances(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 1 || !insts[0].StateMissing() || !slices.Contains(insts[0].MissingRoots, profile.StateRoot) {
		t.Fatalf("expected StateMissing on deleted state dir, got %+v", insts)
	}
}

func TestProtectionBindingDelete(t *testing.T) {
	ctx := context.Background()
	agent := identity.AgentRef{ServerUID: "srv-1", InstallID: "inst-1", InstanceID: "demo", Generation: "gen-1"}
	other := identity.AgentRef{ServerUID: "srv-1", InstallID: "inst-1", InstanceID: "demo", Generation: "gen-2"}

	// Deleting when directory does not exist is a no-op and does not create it.
	absentDir := filepath.Join(t.TempDir(), "absent")
	absentStore := NewProtectionBindingStore(absentDir, nil)
	if err := absentStore.Delete(ctx, agent); err != nil {
		t.Fatalf("delete absent dir: %v", err)
	}
	if _, err := os.Stat(absentDir); !os.IsNotExist(err) {
		t.Fatalf("delete should not create dir: %v", err)
	}

	dir := t.TempDir()
	store := NewProtectionBindingStore(dir, []config.Repo{{Name: "disk", Path: t.TempDir()}})
	if _, err := store.CompareAndSwap(ctx, agent, 0, 1, bindingCopies("disk")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwap(ctx, other, 0, 1, bindingCopies("disk")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, agent); err != nil {
		t.Fatalf("delete first: %v", err)
	}
	if got, err := store.Read(ctx, agent); err != nil || got != nil {
		t.Fatalf("agent should be deleted: %+v %v", got, err)
	}
	if got, err := store.Read(ctx, other); err != nil || got == nil {
		t.Fatalf("other should remain: %+v %v", got, err)
	}
	// Delete remaining entry and ensure file stays valid.
	if err := store.Delete(ctx, other); err != nil {
		t.Fatalf("delete second: %v", err)
	}
	if got, err := store.Read(ctx, other); err != nil || got != nil {
		t.Fatalf("other should be deleted: %+v %v", got, err)
	}
	// Repeating delete is a no-op.
	if err := store.Delete(ctx, other); err != nil {
		t.Fatalf("delete idempotent: %v", err)
	}
}
