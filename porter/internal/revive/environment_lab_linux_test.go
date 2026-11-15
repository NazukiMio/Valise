//go:build linux

package revive

import (
	"context"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/core/revive"
	"valise/porter/internal/isolated"
)

var environmentLab = flag.Bool("environment-test-lab", false, "run synthetic test-stage to isolated-gateway integration on Linux")
var environmentHold = flag.Bool("environment-hold", false, "synthetic gateway holds readiness for interruption tests")

var environmentAcceptOriginal = flag.Bool("environment-accept-original", false, "synthetic original programme data acceptance")

var environmentMutate = flag.Bool("environment-mutate", false, "synthetic probe data write")

var environmentGuest = flag.Bool("environment-guest", false, "synthetic private gateway")

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "environment-probe" {
		if isolated.ProbeGuest(os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestEnvironmentGuest(t *testing.T) {
	if !*environmentGuest {
		t.Skip("private synthetic gateway only")
	}
	content, err := os.ReadFile("SOUL.md")
	if err != nil || string(content) != "snapshot" && (!*environmentAcceptOriginal || string(content) != "current soul" && string(content) != "probe wrote") {
		t.Fatal("gateway did not receive selected snapshot")
	}
	expectedSecret := "base secret"
	if *environmentAcceptOriginal && string(content) == "current soul" {
		expectedSecret = "KEY=current"
	}
	if b, err := os.ReadFile(".env"); err == nil && string(b) != expectedSecret {
		t.Fatal("guest used active or wrong baseline credentials")
	}
	if _, err := os.Lstat("memory/deleted"); !os.IsNotExist(err) {
		t.Fatal("deleted content was restored")
	}
	if *environmentMutate && string(content) == "snapshot" {
		if err := os.WriteFile("SOUL.md", []byte("probe wrote"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Any response is deliberately discarded by the production probe.
	if *environmentHold {
		if err := os.WriteFile("probe-started", []byte("started"), 0600); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Second)
		} // killed with this finite test's private namespace
	}
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, unavailable := os.Stat("gateway-unavailable")
		if *environmentHold || unavailable == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	})
	if err := http.ListenAndServe("127.0.0.1:18391", nil); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentTestStageThroughGatewayAndCleanupLab(t *testing.T) {
	if !*environmentLab {
		t.Skip("requires explicit real Linux namespace lab")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, composed := range []bool{false, true} {
		t.Run(map[bool]string{false: "full", true: "composed"}[composed], func(t *testing.T) {
			var w *world
			var p revive.Plan
			if composed {
				w, p = composeWorld(t, program)
			} else {
				w, p = environmentWorld(t, program)
			}
			files := []snapFile{{"SOUL.md", "snapshot"}}
			if composed {
				files = append(files, snapFile{".env", "base secret"})
			}
			w.stage(t, p, files)
			write(t, filepath.Join(w.home, "unit-state"), "active", 0600)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			result, err := w.r.TestEnvironment(ctx, p.OpID)
			if err != nil || result == nil || !result.CandidateBuilt || result.State != "finished" || !result.Probe.Verified() {
				t.Fatalf("full test result %+v, %v", result, err)
			}
			if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" || read(t, filepath.Join(w.home, "unit-state")) != "active" {
				t.Fatal("isolated test changed active state or service")
			}
			// Query/retry must preserve the original proof without running another
			// program, even after the original executable has changed.
			write(t, filepath.Join(w.state, "bin/gateway"), "changed", 0700)
			again, err := w.r.TestEnvironment(ctx, p.OpID)
			if err != nil || again.ProgramSHA256 != result.ProgramSHA256 || !again.Probe.Verified() {
				t.Fatal("original test was replayed", again, err)
			}
			if _, err := w.r.Resume(ctx, revive.ResumeParams{OpID: p.OpID, Action: "discard"}); err != nil {
				t.Fatal(err)
			}
			if path, err := w.r.testDirectory(p.OpID, false); err != nil || path != "" {
				t.Fatal("test materials left after explicit discard", err)
			}
			j, err := w.r.Store.Load(p.OpID)
			if err != nil || j.EnvironmentTest == nil || !j.EnvironmentTest.Probe.Verified() || j.State != revive.StateRolledBack {
				t.Fatal("cleanup lost original test evidence", err)
			}
		})
	}

}
