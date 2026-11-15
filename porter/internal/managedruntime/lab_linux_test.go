//go:build linux

package managedruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"valise/core/identity"
	"valise/core/profile"
)

var managedLab = flag.Bool("managed-runtime-lab", false, "run an explicitly owned synthetic systemd user runtime")
var managedGuest = flag.Bool("managed-guest", false, "synthetic managed Agent")
var managedPort = flag.Int("managed-port", 0, "synthetic gateway port")

func TestManagedGuest(t *testing.T) {
	if !*managedGuest {
		t.Skip("private synthetic gateway only")
	}
	if _, err := os.Stat("expected"); err != nil {
		t.Fatal("stable data mount missing")
	}
	if err := os.WriteFile("started", []byte("started"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := http.ListenAndServe("127.0.0.1:"+strconv.Itoa(*managedPort), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })); err != nil {
		t.Fatal(err)
	}
}

func TestManagedServiceStableDataAndIndependentProgrammeLab(t *testing.T) {
	if !*managedLab {
		t.Skip("requires explicit real Linux systemd user lab")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	c := Controller{Home: home}
	_, w, spec, bindings := definitionFixture(t)
	programme, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(programme)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.ProgramDir(), "app/gateway"), bytes, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	spec.Argv = []string{spec.Argv[0], "-test.run=TestManagedGuest", "-managed-guest", "-managed-port=" + strconv.Itoa(port)}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	agent := identity.AgentRef{ServerUID: "runtime-lab", InstallID: "fixture", InstanceID: "fixture", Generation: hex.EncodeToString(random[:])}
	d, err := Compile(agent, w, spec, bindings)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := os.WriteFile(filepath.Join(bindings[0].Host, "expected"), []byte("slot-A"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Install(ctx, d, ""); err != nil {
		t.Fatal("cannot install synthetic owned unit", err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		if _, err := c.DisableStartup(cleanup, d); err != nil {
			t.Errorf("cannot disable synthetic startup %s: %v", d.Name(), err)
			return
		}
		stopped, err := c.Stop(cleanup, d)
		if err != nil || !stopped.Observed.Stopped {
			t.Errorf("retained unverified synthetic unit %s: %v", d.Name(), err)
			return
		}
		if err := c.Remove(cleanup, d); err != nil {
			t.Errorf("retained synthetic definition %s: %v", d.Name(), err)
			return
		}
		// The test owns this random key; no production Agent can reference it.
		if err := os.Remove(filepath.Join(home, ".local/state/valise/managed-runtime", d.Name()+".lock")); err != nil {
			t.Error(err)
		}
	}()
	start, err := c.Start(ctx, d)
	if err != nil || !start.CommandExited || start.ExitCode != 0 || !start.Observed.Running || start.Observed.Stopped {
		t.Fatalf("runtime start %+v %v", start, err)
	}
	client := http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	check := func() {
		t.Helper()
		for i := 0; i < 40; i++ {
			resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
			if err == nil {
				status := resp.StatusCode
				resp.Body.Close()
				if status == 200 {
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("synthetic gateway did not respond")
	}
	check()
	declaration := profile.Profile{Entries: []profile.Entry{{ID: "gateway", Kind: "web", Host: "127.0.0.1", Port: port, Path: "/"}}, Health: &profile.Health{Checks: []profile.HealthCheck{{Kind: "http", Entry: "gateway", ExpectStatus: []int{200}}}}, Environments: &profile.Environments{GatewayEntry: "gateway"}}
	evidence, err := c.CheckHTTP(ctx, d, declaration)
	if err != nil || !evidence.GatewayPassed || !evidence.PortOwned || !evidence.HTTPResponded {
		t.Fatalf("owned gateway proof %+v %v", evidence, err)
	}
	unrelated, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })}
	go server.Serve(unrelated)
	defer server.Close()
	if _, err := c.EnableStartup(ctx, d, declaration); err != nil {
		t.Fatal("verified synthetic startup could not be enabled", err)
	}
	if _, err := c.DisableStartup(ctx, d); err != nil {
		t.Fatal("synthetic startup could not be disabled before swap", err)
	}
	declaration.Entries[0].Port = unrelated.Addr().(*net.TCPAddr).Port
	wrong, err := c.CheckHTTP(ctx, d, declaration)
	if err == nil || wrong.GatewayPassed || wrong.PortOwned || wrong.HTTPResponded {
		t.Fatal("unrelated host gateway accepted", wrong, err)
	}
	reloaded, err := c.Load(agent, d.Digest())
	if err != nil || reloaded.Digest() != d.Digest() {
		t.Fatal("cannot query original definition after reconnect", err)
	}
	stillRunning, err := c.Read(ctx, reloaded)
	if err != nil || !stillRunning.Running || stillRunning.MainPID != start.Observed.MainPID {
		t.Fatal("query did not preserve original process", err)
	}
	again, err := c.Start(ctx, reloaded)
	if err != nil || again.Dispatched || again.Observed.MainPID != start.Observed.MainPID {
		t.Fatal("retry restarted the running runtime", err)
	}
	if err := c.Install(ctx, d, d.Digest()); err == nil {
		t.Fatal("definition overwritten while service running")
	}
	stop, err := c.Stop(ctx, d)
	if err != nil || !stop.Observed.Stopped || stop.Observed.Running {
		t.Fatalf("stop not independently verified %+v %v", stop, err)
	}
	if _, err := os.Stat(filepath.Join(bindings[0].Host, "started")); err != nil {
		t.Fatal("programme did not write only its declared data", err)
	}
	// Stable physical path, different incarnation. The transaction layer owns
	// the journal; this synthetic fixture demonstrates the service mechanism.
	sealed := bindings[0].Host + ".sealed-A"
	if err := os.Rename(bindings[0].Host, sealed); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(bindings[0].Host, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bindings[0].Host, "expected"), []byte("slot-B"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(ctx, d); err != nil {
		t.Fatal("stable runtime cannot start exchanged data", err)
	}
	check()
	if b, err := os.ReadFile(filepath.Join(sealed, "expected")); err != nil || string(b) != "slot-A" {
		t.Fatal("sealed data changed", err)
	}
}
