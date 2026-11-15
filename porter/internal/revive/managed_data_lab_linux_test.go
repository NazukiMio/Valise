//go:build linux

package revive

import (
	"context"
	"flag"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/profile"
	"valise/porter/internal/isolated"
	"valise/porter/internal/managedruntime"
)

var managedDataLab = flag.Bool("managed-data-lab", false, "synthetic journaled data exchange with an owned systemd user service")
var managedDataGuest = flag.Bool("managed-data-guest", false, "synthetic data exchange gateway")
var managedDataPort = flag.Int("managed-data-port", 0, "synthetic data exchange port")
var managedDataStatus = flag.Int("managed-data-status", 200, "synthetic gateway status")
var managedDataNested = flag.Bool("managed-data-nested", false, "synthetic logical workspace root")

func TestManagedDataGuest(t *testing.T) {
	if !*managedDataGuest {
		t.Skip("private synthetic gateway only")
	}
	if b, err := os.ReadFile("SOUL.md"); err != nil || string(b) != "current soul" && string(b) != "restored" {
		t.Fatal("wrong synthetic data")
	}
	if *managedDataNested {
		soul, _ := os.ReadFile("SOUL.md")
		file, want := "workspace/original.md", "original workspace"
		if string(soul) == "restored" {
			file, want = "workspace/restored.md", "restored workspace"
		}
		if b, err := os.ReadFile(file); err != nil || string(b) != want {
			t.Fatal("logical workspace not mapped", err)
		}
		if err := os.WriteFile("workspace/service-write", []byte("workspace active"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	status := *managedDataStatus
	if _, err := os.Stat("formal-failure"); err == nil {
		interfaces, err := net.Interfaces()
		if err != nil {
			t.Fatal(err)
		}
		for _, iface := range interfaces {
			if iface.Flags&net.FlagLoopback == 0 {
				status = 503
			}
		}
	}
	if err := os.WriteFile("service-write", []byte(strconv.Itoa(status)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := http.ListenAndServe("127.0.0.1:"+strconv.Itoa(*managedDataPort), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })); err != nil {
		t.Fatal(err)
	}
}

func TestManagedDataOwnedServiceFailureAndRollbackLab(t *testing.T) {
	if !*managedDataLab {
		t.Skip("requires explicitly authorized real Linux systemd user lab")
	}
	w := newManagedWorld(t, false)
	workspace, err := isolated.NewWorkspaceForHome(t.TempDir(), "/home/fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.SetProgramPaths([]string{"app"}); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.ProgramDir(), "app/gateway"), program, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	spec := isolated.Spec{Home: "/home/fixture", WorkingDir: "/home/fixture/state", Path: []string{"/home/fixture/app"},
		Argv: []string{"/home/fixture/app/gateway", "-test.run=TestManagedDataGuest", "-managed-data-guest", "-managed-data-port=" + strconv.Itoa(port)}}
	bindings := []isolated.DataBinding{{Host: w.state, Guest: "/home/fixture/state"}}
	a, err := managedruntime.Compile(w.scope.Agent, workspace, spec, bindings)
	if err != nil {
		t.Fatal(err)
	}
	spec.Argv = append(spec.Argv, "-managed-data-status=503")
	b, err := managedruntime.Compile(w.scope.Agent, workspace, spec, bindings)
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	c := managedruntime.Controller{Home: home}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := c.Install(ctx, a, ""); err != nil {
		t.Fatal(err)
	}
	current := a
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if _, err := c.DisableStartup(cleanup, current); err != nil {
			t.Error("synthetic disable", err)
			return
		}
		if stop, err := c.Stop(cleanup, current); err != nil || !stop.Observed.Stopped {
			t.Error("synthetic stop not verified", err)
			return
		}
		if err := c.Remove(cleanup, current); err != nil {
			t.Error("synthetic unit cleanup", err)
			return
		}
		if err := os.Remove(filepath.Join(home, ".local/state/valise/managed-runtime", current.Name()+".lock")); err != nil {
			t.Error(err)
		}
	}()
	declaration := profile.Profile{Entries: []profile.Entry{{ID: "gateway", Kind: "web", Host: "127.0.0.1", Port: port, Path: "/"}},
		Health: &profile.Health{Checks: []profile.HealthCheck{{Kind: "http", Entry: "gateway", ExpectStatus: []int{200}}}}, Environments: &profile.Environments{GatewayEntry: "gateway"}}
	awaitHTTP := func(d managedruntime.Definition, status int) {
		t.Helper()
		for i := 0; i < 40; i++ {
			proof, err := c.CheckHTTP(ctx, d, declaration)
			if proof.HTTPResponded && proof.PortOwned && proof.HTTPStatus == status && (status != 200 || err == nil && proof.GatewayPassed) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("owned synthetic HTTP did not reach expected status", status)
	}
	if start, err := c.Start(ctx, a); err != nil || !start.Observed.Running {
		t.Fatal(start, err)
	}
	awaitHTTP(a, 200)
	// Transfer all candidate data while A remains active, then verify no restart.
	w.begin(t)
	before, err := c.Read(ctx, a)
	if err != nil || !before.Running || read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("preparation changed active environment", err)
	}
	w.transition(t, environment.Registry.Prepared)
	if _, err := c.DisableStartup(ctx, a); err != nil {
		t.Fatal(err)
	}
	w.transition(t, environment.Registry.StopOriginal)
	if stopped, err := c.Stop(ctx, a); err != nil || !stopped.Observed.Stopped {
		t.Fatal(stopped, err)
	}
	w.transition(t, environment.Registry.OriginalStopped)
	if _, err := w.r.ExchangeManagedData(ctx, w.p.OpID); err != nil {
		t.Fatal(err)
	}
	if err := c.Install(ctx, b, a.Digest()); err != nil {
		t.Fatal(err)
	}
	current = b
	w.transition(t, environment.Registry.StartCandidate)
	if start, err := c.Start(ctx, b); err != nil || !start.Observed.Running {
		t.Fatal(start, err)
	}
	awaitHTTP(b, 503)
	proof, err := c.CheckHTTP(ctx, b, declaration)
	if err == nil || proof.GatewayPassed {
		t.Fatal("failed candidate passed formal gateway", proof, err)
	}
	w.transition(t, func(r environment.Registry, id string) (environment.Registry, error) {
		return r.BeginRollback(id, "gateway_failed")
	})
	if stop, err := c.Stop(ctx, b); err != nil || !stop.Observed.Stopped {
		t.Fatal(stop, err)
	}
	w.transition(t, environment.Registry.CandidateStopped)
	if _, err := w.r.RevertManagedData(ctx, w.p.OpID); err != nil {
		t.Fatal(err)
	}
	if err := c.Install(ctx, a, b.Digest()); err != nil {
		t.Fatal(err)
	}
	current = a
	w.transition(t, environment.Registry.ResumeOriginal)
	if _, err := c.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	awaitHTTP(a, 200)
	w.transition(t, environment.Registry.OriginalHealthy)
	j, _ := w.r.Store.Load(w.p.OpID)
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" || read(t, filepath.Join(j.Roots[0].Failed, "service-write")) != "503" {
		t.Fatal("original or failed candidate data not preserved")
	}
}
