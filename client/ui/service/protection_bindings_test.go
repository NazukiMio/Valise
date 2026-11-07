package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"valise/client/config"
	"valise/core/protection"
)

func bindingCopies(repo string) []protection.LocalCopy {
	enabled, disabled := true, false
	return []protection.LocalCopy{{RepoID: repo, AfterBackup: &enabled, OnClientOpen: &disabled}}
}

func TestProtectionBindingReadDoesNotInitializeAndCAS(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	repos := []config.Repo{{Name: "disk", Path: "/not-needed-for-config"}}
	s := NewProtectionBindingStore(dir, repos)
	agent := protectionRequest().AgentRef
	old, err := s.Read(context.Background(), agent)
	if err != nil || old != nil {
		t.Fatalf("absent: %+v %v", old, err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("read initialized state: %v", err)
	}
	first, err := s.CompareAndSwap(context.Background(), agent, 0, 2, bindingCopies("disk"))
	if err != nil || first.Revision != 1 || first.ServerRevisionSeen != 2 || !protection.ValidID(first.ClientID) {
		t.Fatalf("first CAS: %+v %v", first, err)
	}
	stale := NewProtectionBindingStore(dir, repos)
	_, err = stale.CompareAndSwap(context.Background(), agent, 0, 2, bindingCopies("disk"))
	if journeyCode(err) != "revision_conflict" {
		t.Fatalf("stale CAS: %v", err)
	}
	// Updating the binding does not need to open or create the repository path.
	second, err := stale.CompareAndSwap(context.Background(), agent, 1, 3, []protection.LocalCopy{})
	if err != nil || second.Revision != 2 || second.ServerRevisionSeen != 3 || second.ClientID != first.ClientID {
		t.Fatalf("next CAS: %+v %v", second, err)
	}
	read, err := s.Read(context.Background(), agent)
	if err != nil || read.Revision != 2 || len(read.LocalCopies) != 0 {
		t.Fatalf("read: %+v %v", read, err)
	}
	// A different generation is a different binding, even at the same instance ID.
	agent.Generation = "next-generation"
	another, err := s.CompareAndSwap(context.Background(), agent, 0, 3, bindingCopies("disk"))
	if err != nil || another.Revision != 1 || another.ClientID != first.ClientID {
		t.Fatalf("new generation: %+v %v", another, err)
	}
	st, err := os.Stat(filepath.Join(dir, "protection-bindings.json"))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("private binding permissions: %v %v", st, err)
	}
}

func TestProtectionBindingRejectsUnknownRepoAndSchema(t *testing.T) {
	dir := t.TempDir()
	s := NewProtectionBindingStore(dir, []config.Repo{{Name: "disk"}})
	agent := protectionRequest().AgentRef
	_, err := s.CompareAndSwap(context.Background(), agent, 0, 1, bindingCopies("unknown"))
	if journeyCode(err) != "repo_unknown" {
		t.Fatalf("unknown repo: %v", err)
	}
	_, err = s.CompareAndSwap(context.Background(), agent, 0, 1, nil)
	if journeyCode(err) != "request_invalid" {
		t.Fatalf("missing full choice: %v", err)
	}
	_, err = s.CompareAndSwap(context.Background(), agent, 0, 1, bindingCopies("disk"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "protection-bindings.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	doc["automatic_prune"] = true
	b, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.Read(context.Background(), agent)
	if journeyCode(err) != "unsupported_schema" {
		t.Fatalf("unknown schema field: %v", err)
	}
	_, err = s.CompareAndSwap(context.Background(), agent, 1, 2, bindingCopies("disk"))
	if journeyCode(err) != "unsupported_schema" {
		t.Fatalf("unknown schema overwritten: %v", err)
	}
}

func TestProtectionBindingConcurrentCASAndCancellation(t *testing.T) {
	dir := t.TempDir()
	repos := []config.Repo{{Name: "disk"}}
	agent := protectionRequest().AgentRef
	a, b := NewProtectionBindingStore(dir, repos), NewProtectionBindingStore(dir, repos)
	var wg sync.WaitGroup
	wg.Add(2)
	outcomes := make(chan error, 2)
	for _, store := range []*ProtectionBindingStore{a, b} {
		go func(s *ProtectionBindingStore) {
			defer wg.Done()
			_, err := s.CompareAndSwap(context.Background(), agent, 0, 1, bindingCopies("disk"))
			outcomes <- err
		}(store)
	}
	wg.Wait()
	close(outcomes)
	successes := 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if code := journeyCode(err); code != "revision_conflict" && code != "resource_busy" {
			t.Fatalf("unexpected contention: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("two writers published same expected revision: %d", successes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.CompareAndSwap(ctx, agent, 1, 2, bindingCopies("disk")); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := a.Read(ctx, agent); err != context.Canceled {
		t.Fatal(err)
	}
}
