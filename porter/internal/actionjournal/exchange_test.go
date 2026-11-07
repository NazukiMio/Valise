package actionjournal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/action"
	"valise/core/profile"
)

func exchangeFixture(t *testing.T) (Store, *action.Operation) {
	t.Helper()
	home := t.TempDir()
	s := Store{Home: home}
	live := filepath.Join(home, "live")
	id := "act_" + strings.Repeat("d", 32)
	private := filepath.Join(home, ".valise-action-"+id)
	if err := os.Mkdir(live, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(private, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	for p, body := range map[string]string{live: "original", stage: "configured"} {
		if err := os.WriteFile(filepath.Join(p, "config"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	li, _ := DirectoryIdentity(live)
	si, _ := DirectoryIdentity(stage)
	zero := 0
	j := &action.Operation{ID: id, Format: action.OperationFormat, Plan: action.Plan{Live: live, LiveIdentity: li, Definition: profile.Action{Steps: []profile.ActionStep{{ID: "policy"}}}}, Stage: stage, Previous: filepath.Join(private, "prev"), StageIdentity: si, StageVerified: true, Phase: "ready", Steps: []action.StepResult{{ID: "policy", Started: true, Completed: true, Exit: &zero}}}
	j.StageTreeDigest, _ = TreeDigest(stage)
	j.LiveTreeDigest, _ = TreeDigest(live)
	if err := s.Save(j); err != nil {
		t.Fatal(err)
	}
	return s, j
}
func TestPublishAndIdempotentRecovery(t *testing.T) {
	s, j := exchangeFixture(t)
	if err := s.Publish(j); err != nil {
		t.Fatal(err)
	}
	if !j.Published {
		t.Fatal("not published")
	}
	for p, want := range map[string]string{j.Plan.Live: "configured", j.Previous: "original"} {
		b, err := os.ReadFile(filepath.Join(p, "config"))
		if err != nil || string(b) != want {
			t.Fatal(p, string(b), err)
		}
	}
	if err := s.Publish(j); err != nil {
		t.Fatal("repeat publish", err)
	}
}
func TestRecoverRenameWindows(t *testing.T) {
	for _, phase := range []string{"move-live-intent", "publish-intent"} {
		for _, second := range []bool{false, true} {
			if second && phase != "publish-intent" {
				continue
			}
			t.Run(phase+map[bool]string{false: "-old-moved", true: "-new-moved"}[second], func(t *testing.T) {
				s, j := exchangeFixture(t)
				j.Phase = phase
				if err := s.Save(j); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(j.Plan.Live, j.Previous); err != nil {
					t.Fatal(err)
				}
				if second {
					if err := os.Rename(j.Stage, j.Plan.Live); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.Reconcile(j); err != nil {
					t.Fatal(err)
				}
				want := "original"
				if second {
					want = "configured"
				}
				b, err := os.ReadFile(filepath.Join(j.Plan.Live, "config"))
				if err != nil || string(b) != want || j.Published != second {
					t.Fatal(string(b), j.Published, err)
				}
			})
		}
	}
}
func TestConflictingIdentityIsPreserved(t *testing.T) {
	s, j := exchangeFixture(t)
	if err := os.Rename(j.Plan.Live, j.Previous); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(j.Plan.Live, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Reconcile(j); err == nil {
		t.Fatal("unknown live identity accepted")
	}
	if _, err := os.Stat(j.Previous); err != nil {
		t.Fatal("old data removed", err)
	}
}
func TestUnknownStepCannotPublish(t *testing.T) {
	s, j := exchangeFixture(t)
	j.Steps[0].Completed = false
	if err := s.Publish(j); err == nil {
		t.Fatal("unknown step published")
	}
	b, err := os.ReadFile(filepath.Join(j.Plan.Live, "config"))
	if err != nil || string(b) != "original" {
		t.Fatal(string(b), err)
	}
}
