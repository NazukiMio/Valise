package enroll

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"valise/core/enroll"
	model "valise/core/identity"
	"valise/core/profile"
	identitystore "valise/porter/internal/identity"
)

func TestAcceptRecordGenerationLifecycle(t *testing.T) {
	home := t.TempDir()
	p := profile.Profile{ID: "demo", ProfileVersion: 1}
	r := enroll.Record{InstanceID: "demo", ProfileID: p.ID, ProfileVersion: 1, Roots: map[string]string{"state": "/home/test/state"}, Env: map[string]string{"TOKEN": "never-save-in-identity"}}
	// Ordinary fact writes, including legacy and restore paths, remain unbound.
	if _, err := WriteRecord(home, r, p); err != nil {
		t.Fatal(err)
	}
	b, err := identitystore.Read(home, r.InstanceID)
	if err != nil || b.IdentityState != model.LegacyUnbound {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err := os.Stat(identitystore.Dir(home)); !os.IsNotExist(err) {
		t.Fatal("fact write initialized identity")
	}
	_, a, err := AcceptRecord(home, r, p)
	if err != nil {
		t.Fatal(err)
	}
	first := a.Ref
	p.ProfileVersion, r.ProfileVersion = 2, 2
	r.Roots["state"] = "/home/test/accepted-new-root"
	r.DisplayName = "Renamed"
	_, a, err = AcceptRecord(home, r, p)
	if err != nil || a.Ref != first || a.Roots["state"] != r.Roots["state"] {
		t.Fatalf("continuous enrollment rotated identity: %+v %v", a, err)
	}
	if err := MarkSuperseded(home, r.InstanceID, enroll.Supersession{}); err != nil {
		t.Fatal(err)
	}
	_, a, err = AcceptRecord(home, r, p)
	if err != nil || a.Ref.Generation == first.Generation || a.Ref.ServerUID != first.ServerUID {
		t.Fatalf("superseded enrollment: %+v %v", a, err)
	}
	second := a.Ref
	if err := RemoveRecord(home, r.InstanceID); err != nil {
		t.Fatal(err)
	}
	_, a, err = AcceptRecord(home, r, p)
	if err != nil || a.Ref.Generation == second.Generation || a.Ref.ServerUID != first.ServerUID {
		t.Fatalf("reused id: %+v %v", a, err)
	}
	for _, name := range []string{"agents/demo.json", "commit.json"} {
		raw, err := os.ReadFile(identitystore.Dir(home) + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "never-save-in-identity") || strings.Contains(string(raw), "TOKEN") {
			t.Fatal("identity stores environment values")
		}
	}
}

func TestConcurrentAcceptRecordKeepsAgentsAndGeneration(t *testing.T) {
	home := t.TempDir()
	p := profile.Profile{ID: "demo", ProfileVersion: 1}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "demo-" + string(rune('a'+i))
			r := enroll.Record{InstanceID: id, Name: id, ProfileID: p.ID, ProfileVersion: 1, Roots: map[string]string{"state": "/home/test/" + id}}
			_, a, err := AcceptRecord(home, r, p)
			if err != nil {
				errs <- err
				return
			}
			r.EnrolledAt = time.Now().UTC()
			_, again, err := AcceptRecord(home, r, p)
			if err != nil {
				errs <- err
				return
			}
			if a.Ref != again.Ref {
				errs <- &changedGeneration{}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	records, err := ListRecords(home)
	if err != nil || len(records) != 8 {
		t.Fatalf("concurrent dossier lost agents: %d %v", len(records), err)
	}
}

type changedGeneration struct{}

func (*changedGeneration) Error() string { return "concurrent reaccept changed generation" }
