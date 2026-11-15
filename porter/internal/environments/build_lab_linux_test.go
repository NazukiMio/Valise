package environments

// These explicitly opted-in tests require a real Linux kernel and bwrap.
// They use only a synthetic program and synthetic data, never a real Agent.
import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"valise/core/identity"
	"valise/core/vault"
	"valise/porter/internal/isolated"
)

var (
	candidateLab  = flag.Bool("candidate-lab", false, "run private candidate build/gateway lab")
	harnessMode   = flag.String("candidate-harness", "", "synthetic candidate gateway behavior")
	detachedChild = flag.Bool("candidate-child", false, "synthetic detached descendant")
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "environment-probe" {
		if isolated.ProbeGuest(os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCandidateHarness(t *testing.T) {
	if *detachedChild {
		for {
			if err := os.WriteFile("heartbeat", []byte(time.Now().String()), 0o600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	if *harnessMode == "" {
		t.Skip("synthetic guest only")
	}
	// Emulate noisy harnesses. Neither stream nor response bytes may escape
	// into the structured probe result or an error message.
	const marker = "synthetic-credential-never-capture"
	io.WriteString(os.Stdout, marker)
	io.WriteString(os.Stderr, marker)
	if *harnessMode == "exit" {
		return
	}
	if fd, err := os.OpenFile("/proc/"+strconv.Itoa(os.Getppid())+"/fd/1", os.O_WRONLY, 0); err == nil {
		fd.Close()
		t.Fatal("candidate could access controller result channel")
	}
	if b, err := os.ReadFile("memory/required"); err != nil || string(b) != "selected point" || os.Getenv("HOME") != "/source/home" {
		t.Fatal("candidate was not reconstructed from its selected data")
	}
	if err := os.WriteFile("program/unwanted", []byte("must not alter programs"), 0o600); err == nil {
		t.Fatal("program mount was writable")
	}
	child := exec.Command("./program/agent", "-test.run=^TestCandidateHarness$", "-candidate-child")
	child.Dir = "."
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait until the detached process has run, so cleanup is actually tested.
	for until := time.Now().Add(time.Second); ; {
		if _, err := os.Stat("heartbeat"); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("detached fixture did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if strings.HasPrefix(*harnessMode, "offline_") {
		serveOfflineFixture(t, *harnessMode)
		return
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *harnessMode == "bad_status" {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else if *harnessMode == "redirect" && r.URL.Path == "/" {
			w.Header().Set("Location", "/final")
			w.WriteHeader(http.StatusFound)
		}
		w.Header().Set("Set-Cookie", marker)
		io.WriteString(w, marker)
	})
	// This is the synthetic harness listener, not a porter listener.
	if err := http.ListenAndServe("127.0.0.1:18391", handler); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateCandidateGatewayLab(t *testing.T) {
	if !*candidateLab {
		t.Skip("requires explicit real Linux lab run")
	}
	// An unrelated host gateway is deliberately available at the SAME port.
	// A missing/failed private program must never pass by probing this server.
	listener, err := net.Listen("tcp", "127.0.0.1:18391")
	if err != nil {
		t.Fatal("fixture port already in use; existing service was not changed", err)
	}
	var hostRequests atomic.Int64
	host := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hostRequests.Add(1)
		io.WriteString(w, "active fixture")
	})}
	go host.Serve(listener)
	defer host.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "bad_status", "redirect", "exit"} {
		t.Run(mode, func(t *testing.T) {
			in := fixtureInput(t, binary)
			if mode == "success" {
				addProgramEntryLink(t, &in, binary)
			}
			in.Profile.Environments.StartArgv = append(in.Profile.Environments.StartArgv, "-test.run=^TestCandidateHarness$", "-candidate-harness="+mode)
			in.Target.Source.ProfileDigest = Digest(in.Profile)
			in.Target.Files.Files = []vault.File{dataFile("state", "memory/required", "memory", "selected point")}
			in.OpenData = func(context.Context, identity.SnapshotRef, string, string) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("selected point")), nil
			}
			candidate, err := Build(context.Background(), t.TempDir(), in)
			if err != nil {
				t.Fatal(err)
			}
			check, err := isolated.CheckFor(in.Profile)
			if err != nil {
				t.Fatal(err)
			}
			check.Timeout = time.Second
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			result, err := isolated.GatewayProbe(ctx, candidate.Workspace, candidate.Spec, check)
			if err != nil {
				t.Fatal("isolated gateway probe", err)
			}
			if !result.Completed || !result.ProcessesStopped || !result.ProcessStarted {
				t.Fatal("incomplete preparation or process cleanup", result)
			}
			if result.Verified() != (mode == "success") {
				t.Fatal("wrong readiness result", result)
			}
			if mode == "exit" && (result.FailureCode != "program_exited" || result.GatewayPassed) {
				t.Fatal("exit code or host gateway replaced actual readiness", result)
			}
			if mode == "redirect" && result.HTTPStatus != http.StatusFound {
				t.Fatal("redirect was followed", result)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "synthetic-credential") || hostRequests.Load() != 0 {
				t.Fatal("child output leaked or host gateway was probed")
			}
			if mode != "exit" {
				beat := filepath.Join(candidate.Workspace.HomeDir(), ".fixture/heartbeat")
				before, err := os.ReadFile(beat)
				if err != nil {
					t.Fatal(err)
				}
				time.Sleep(150 * time.Millisecond)
				after, err := os.ReadFile(beat)
				if err != nil || string(before) != string(after) {
					t.Fatal("detached candidate process survived")
				}
			}
			// The prepared workspace is one operation, not a reusable command
			// runner. A second start cannot silently replay the probe.
			if _, err := isolated.GatewayProbe(ctx, candidate.Workspace, candidate.Spec, check); err == nil {
				t.Fatal("replayed the same candidate probe")
			}
		})
	}
}
