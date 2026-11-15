//go:build linux

package revive

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"valise/core/enroll"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
)

var nativePTYReady = flag.String("native-pty-ready", "", "synthetic SSH PTY coordination file")
var nativePTYDone = flag.String("native-pty-done", "", "synthetic SSH PTY completion file")
var nativePTYGuest = flag.Bool("native-pty-guest", false, "synthetic native SSH PTY guest")

func TestNativePTYGuest(t *testing.T) {
	if !*nativePTYGuest {
		t.Skip("synthetic real-SSH PTY guest only")
	}
	mode, err := unix.IoctlGetTermios(0, unix.TCGETS)
	if err != nil || mode.Iflag&unix.ICRNL == 0 || mode.Lflag&unix.ICANON == 0 {
		t.Fatal("native stdin lacks cooked PTY modes")
	}
	size, err := unix.IoctlGetWinsize(0, unix.TIOCGWINSZ)
	if err != nil || size.Row == 0 || size.Col == 0 {
		t.Fatal("native PTY has no usable size")
	}
	fmt.Fprintln(os.Stdout, "NATIVE-PTY-READY")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil || line != "native-pty-input\n" {
		t.Fatal("native Enter/line input did not reach guest")
	}
	fmt.Fprintln(os.Stdout, "NATIVE-PTY-PASSED")
}

// This explicit lab worker keeps its T.TempDir alive while the host starts a
// real fresh ssh -tt connection to the normal porter native entry. It creates
// no service or real Agent; the other private-runtime tests cover A/B binding.
func TestNativePTYFixtureWorker(t *testing.T) {
	if !*activationLab || *nativePTYReady == "" || *nativePTYDone == "" {
		t.Skip("explicit real-SSH PTY coordination only")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := profile.Profile{ID: "fixture", ProfileVersion: 1, NativeCLI: profile.Argv{exe, "-test.run=^TestNativePTYGuest$", "-native-pty-guest"}}
	if _, _, err := porterenroll.AcceptRecord(home, enroll.Record{InstanceID: "fixture", ProfileID: p.ID, ProfileVersion: 1, Roots: map[string]string{"state": state}, EnrolledAt: time.Now().UTC()}, p); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"home": home, "instance": "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(*nativePTYReady, raw, 0600); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(2 * time.Minute)
	for time.Now().Before(until) {
		if _, err := os.Lstat(*nativePTYDone); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("real-SSH PTY coordinator did not finish")
}
