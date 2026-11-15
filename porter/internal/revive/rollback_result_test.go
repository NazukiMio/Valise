package revive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/revive"
)

func rollbackWorld(t *testing.T) (*world, *revive.Journal) {
	t.Helper()
	w := newDirectWorld(t)
	p := w.plan()
	w.stage(t, p, snapshot)
	j, err := w.r.Store.Load(p.OpID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(j.Roots[0].Live, j.Roots[0].Prev); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(j.Roots[0].Stage, j.Roots[0].Live); err != nil {
		t.Fatal(err)
	}
	j.State, j.ServiceWasActive = revive.StateStarted, true
	if err := w.r.Store.Save(j); err != nil {
		t.Fatal(err)
	}
	return w, j
}

func TestRollbackFailuresKeepJournalAndCanResume(t *testing.T) {
	for _, operation := range []string{"stop", "start", "program", "journal"} {
		t.Run(operation, func(t *testing.T) {
			w, j := rollbackWorld(t)
			script := fakeSystemctl
			code := "rollback_" + operation + "_failed"
			switch operation {
			case "stop", "start":
				script = strings.Replace(script, operation+")", operation+") exit 1 ;;\nunused)", 1)
				write(t, filepath.Join(w.bin, "systemctl"), script, 0755)
			case "program":
				j.NPMInstalled, j.NPMOldVersion = true, "1.0.0"
				j.Plan.Body.Package, j.Plan.Body.Version = "demo", "2.0.0"
				write(t, filepath.Join(w.bin, "npm"), "#!/bin/sh\nexit 1\n", 0755)
				if err := w.r.Store.Save(j); err != nil {
					t.Fatal(err)
				}
			case "journal":
				write(t, w.r.Store.DoneDir(), "block archive", 0600)
			}
			res, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: j.Plan.OpID, Action: "back"})
			if err == nil || res == nil || res.Rollback == nil || res.Rollback.Problem != code {
				t.Fatalf("failure lost: %+v %v", res, err)
			}
			if _, err := os.Stat(w.r.Store.path(j.Plan.OpID)); err != nil {
				t.Fatal("unfinished journal lost", err)
			}
			if operation == "stop" {
				if read(t, filepath.Join(w.state, "SOUL.md")) != "old soul" || res.Rollback.Directories != "unknown" {
					t.Fatal("moved directories despite failed stop")
				}
			} else if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" || res.Rollback.Directories != "restored" {
				t.Fatal("original directories not restored")
			}
			if operation == "journal" {
				if err := os.Remove(w.r.Store.DoneDir()); err != nil {
					t.Fatal(err)
				}
			}
			write(t, filepath.Join(w.bin, "systemctl"), fakeSystemctl, 0755)
			write(t, filepath.Join(w.bin, "npm"), "#!/bin/sh\nexit 0\n", 0755)
			res, err = w.r.Resume(context.Background(), revive.ResumeParams{OpID: j.Plan.OpID, Action: "back"})
			if err != nil || res.State != revive.StateRolledBack || res.Rollback.Problem != "" || res.Rollback.Health != "passed" {
				t.Fatalf("resume %+v %v", res, err)
			}
			if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
				t.Fatal("retry replayed directory swap")
			}
		})
	}
}

func TestRollbackHealthFailureRemainsSeparateFromDirectoryResult(t *testing.T) {
	w, j := rollbackWorld(t)
	write(t, filepath.Join(w.home, "unit-broken"), "yes", 0600)
	res, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: j.Plan.OpID, Action: "back"})
	if err != nil || res.State != revive.StateRolledBack || res.Rollback.Directories != "restored" || res.Rollback.Service != "started" || res.Rollback.Health != "failed" || res.Rollback.Problem != "rollback_health_failed" {
		t.Fatalf("health outcome %+v %v", res, err)
	}
}
