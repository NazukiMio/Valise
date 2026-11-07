package actionjournal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/action"
)

func TestDurableOperationLifecycle(t *testing.T) {
	s := Store{Home: t.TempDir()}
	j := &action.Operation{Format: action.OperationFormat, ID: "act_" + strings.Repeat("a", 32), Phase: "planned"}
	if err := s.Save(j); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(s.path(j.ID, false))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, err)
	}
	d, err := os.Stat(s.Dir())
	if err != nil || d.Mode().Perm() != 0700 {
		t.Fatal(d, err)
	}
	if err = s.Check(""); err == nil {
		t.Fatal("unfinished operation did not block writes")
	}
	if err = s.Check(j.ID); err != nil {
		t.Fatal(err)
	}
	j.Phase = "step-started"
	j.Steps = []action.StepResult{{ID: "policy", Started: true}}
	if err = s.Save(j); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(j.ID)
	if err != nil || !got.Steps[0].Started || got.Steps[0].Completed {
		t.Fatal(got, err)
	}
	j.Done = true
	j.Phase = "abandoned"
	if err = s.Save(j); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.path(j.ID, false)); !os.IsNotExist(err) {
		t.Fatal("pending record remains", err)
	}
	if err = s.Check(""); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load(j.ID)
	if err != nil || !got.Done {
		t.Fatal(got, err)
	}
	if err = s.Save(j); err != nil {
		t.Fatal("repeat terminal save", err)
	}
}

func TestCorruptOrFutureOperationBlocks(t *testing.T) {
	for _, body := range []string{`{`, `{"format":"future","id":"ID"}`, `{"format":"valise-action-operation/1","id":"ID","surprise":1}`, `{"format":"valise-action-operation/1","id":"ID"} {}`} {
		t.Run(body, func(t *testing.T) {
			s := Store{Home: t.TempDir()}
			id := "act_" + strings.Repeat("b", 32)
			if err := os.MkdirAll(s.Dir(), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(s.path(id, false), []byte(strings.ReplaceAll(body, "ID", id)), 0600); err != nil {
				t.Fatal(err)
			}
			if err := s.Check(""); err == nil {
				t.Fatal("corrupt record allowed mutation")
			}
		})
	}
}
func TestOperationPathsCannotEscape(t *testing.T) {
	s := Store{Home: t.TempDir()}
	if _, err := s.Load("../escape"); err == nil {
		t.Fatal("invalid id allowed")
	}
	if err := s.Save(&action.Operation{ID: "../escape", Format: action.OperationFormat}); err == nil {
		t.Fatal("invalid id allowed")
	}
	if err := os.MkdirAll(s.Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	id := "act_" + strings.Repeat("c", 32)
	target := filepath.Join(s.Home, "outside")
	if err := os.WriteFile(target, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, s.path(id, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(id); err == nil {
		t.Fatal("symlink journal accepted")
	}
}
