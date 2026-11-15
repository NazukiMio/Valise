package isolated

// Real Linux kernel boundary checks. Ordinary test runs skip these; fake-ssh
// cannot prove namespace isolation. No real harness or credentials are loaded.
import (
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

var (
	lab         = flag.Bool("isolated-lab", false, "run actual Linux isolation checks")
	guest       = flag.Bool("isolated-guest", false, "synthetic gateway inside isolated environment")
	block       = flag.Bool("isolated-block", false, "synthetic task waits for cancellation")
	orphan      = flag.Bool("isolated-orphan", false, "synthetic background child")
	hostAddress = flag.String("isolated-host-address", "", "fixture host listener")
	hostFile    = flag.String("isolated-host-file", "", "fixture inaccessible host file")
	hostNetNS   = flag.String("isolated-host-netns", "", "fixture host namespace identifier")
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "environment-probe" {
		if ProbeGuest(os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestIsolatedGuest(t *testing.T) {
	if !*guest && !*block && !*orphan {
		t.Skip("synthetic guest only")
	}
	if *orphan {
		for {
			if err := os.WriteFile(Home+"/heartbeat", []byte(time.Now().String()), 0o600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(40 * time.Millisecond)
		}
	}
	if *block {
		cmd := exec.Command(Home+"/guest", "-test.run=^TestIsolatedGuest$", "-isolated-orphan")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	if _, err := os.Stat(*hostFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("host file visible")
	}
	for _, key := range []string{"SSH_AUTH_SOCK", "DBUS_SESSION_BUS_ADDRESS", "VALISE_FIXTURE_SECRET", "HTTP_PROXY"} {
		if os.Getenv(key) != "" {
			t.Fatal("inherited session environment", key)
		}
	}
	if os.Getenv("HOME") != Home {
		t.Fatal("private HOME missing")
	}
	if ns, err := os.Readlink("/proc/self/ns/net"); err != nil || ns == *hostNetNS {
		t.Fatal("host network namespace shared")
	}
	if err := unix.Unshare(unix.CLONE_NEWUSER); err == nil {
		t.Fatal("nested user namespaces allowed")
	}
	if c, err := net.DialTimeout("tcp", *hostAddress, 150*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("host listener accessible")
	}
	if err := os.WriteFile("/usr/valise-write-probe", []byte("probe"), 0o600); err == nil {
		t.Fatal("private root writable")
	}
	if err := os.WriteFile("/usr/bin/valise-write-probe", []byte("probe"), 0o600); err == nil {
		t.Fatal("host OS directory writable")
	}
	if err := os.WriteFile(Home+"/private-write", []byte("private fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/run/user", "/tmp/host-fixture", "/root", "/home/parallels"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("host path visible", p)
		}
	}
	// Only this synthetic test fixture listens. Porter itself has no listener.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "gateway fixture") })}
	go s.Serve(l)
	defer s.Close()
	client := http.Client{Timeout: time.Second, Transport: &http.Transport{}}
	resp, err := client.Get("http://" + l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || string(b) != "gateway fixture" {
		t.Fatal("private gateway failed")
	}
}

func TestLinuxBoundary(t *testing.T) {
	if !*lab {
		t.Skip("requires explicit real Linux lab run")
	}
	t.Setenv("SSH_AUTH_SOCK", "/host/login/session")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/host/session")
	t.Setenv("VALISE_FIXTURE_SECRET", "synthetic-do-not-inherit")
	w, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.HomeDir(), "guest"), binary, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "host-only")
	if err := os.WriteFile(sentinel, []byte("active fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{WorkingDir: Home, Argv: []string{Home + "/guest", "-test.run=^TestIsolatedGuest$", "-isolated-guest",
		"-isolated-host-address=" + l.Addr().String(), "-isolated-host-file=" + sentinel, "-isolated-host-netns=" + ns}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Run(ctx, w, spec); err != nil {
		// This command contains exclusively synthetic fixtures, so diagnostic
		// test output is permitted here. Production Run always discards output.
		args, _ := arguments(w, spec)
		out, _ := exec.CommandContext(ctx, "/usr/bin/bwrap", args...).CombinedOutput()
		t.Fatalf("boundary probe: %v; synthetic diagnostics: %s", err, out)
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "active fixture" {
		t.Fatal("active host data changed")
	}
	if b, err := os.ReadFile(filepath.Join(w.HomeDir(), "private-write")); err != nil || !strings.Contains(string(b), "private") {
		t.Fatal("private writes not preserved")
	}
	// Verify a cancelled finite task cannot leave its background child running.
	spec.Argv = []string{Home + "/guest", "-test.run=^TestIsolatedGuest$", "-isolated-block"}
	cancelCtx, stop := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer stop()
	if err := Run(cancelCtx, w, spec); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("cancellation failed", err)
	}
	beat := filepath.Join(w.HomeDir(), "heartbeat")
	before, err := os.ReadFile(beat)
	if err != nil {
		t.Fatal("background child did not start", err)
	}
	time.Sleep(300 * time.Millisecond)
	after, err := os.ReadFile(beat)
	if err != nil || string(before) != string(after) {
		t.Fatal("background child survived cancellation")
	}
}
