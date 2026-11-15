package activeexec_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/profile"
	"valise/porter/internal/activeexec"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	"valise/porter/internal/seal"
)

func TestTypedNativeUsesAcceptedInstanceAndCannotFallBackPastOwnership(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	p := profile.Profile{ID: "fixture", ProfileVersion: 1, NativeCLI: profile.Argv{"/bin/cat"}, NativeHints: []string{"fixture hint"}}
	_, a, err := porterenroll.AcceptRecord(home, enroll.Record{InstanceID: "fixture", ProfileID: p.ID, ProfileVersion: 1, Roots: map[string]string{"state": root}, EnrolledAt: time.Now().UTC()}, p)
	if err != nil {
		t.Fatal(err)
	}
	info, err := activeexec.Info(home, "fixture")
	if err != nil || len(info.Argv) != 1 || info.Argv[0] != "/bin/cat" || len(info.Hints) != 1 {
		t.Fatal(info, err)
	}
	ctx := context.Background()
	lock, unlock, err := (seal.Paths{Home: home}).ActionLock("")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	controller := activeexec.Controller{Home: home}
	cmd, display, err := controller.Native(ctx, "fixture", lock)
	if err != nil || len(display) != 1 {
		t.Fatal(display, err)
	}
	var out bytes.Buffer
	cmd.Stdin, cmd.Stdout = bytes.NewBufferString("native fixture input"), &out
	if err := cmd.Run(); err != nil || out.String() != "native fixture input" {
		t.Fatal(out.String(), err)
	}
	if cmd, _, err := controller.Native(ctx, "fixture", nil); err == nil || cmd != nil {
		t.Fatal("missing lock accepted", cmd, err)
	}
	if _, err := (environments.Store{Home: home}).Update(a.Ref, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(a.Ref) }); err != nil {
		t.Fatal(err)
	}
	if cmd, _, err := controller.Native(ctx, "fixture", lock); err == nil || cmd != nil {
		t.Fatal("unknown managed source borrowed native PATH", cmd, err)
	}
}
