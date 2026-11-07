package seal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vaultrpc"
)

// target_check logs in once, read-only, and reports a failure as a
// classified result, not an error (N2 G.3).
func TestCheckTarget(t *testing.T) {
	w := newWorld(t)
	bin := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in down:*) echo 'ssh: connect to host down port 22: Connection refused' >&2; exit 255;; esac; done\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	w.env.Vars = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	ctx := context.Background()
	up := vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "up:/vault/"}
	res, err := CheckTarget(ctx, w.env, w.p, vaultrpc.TargetCheckParams{Target: &up})
	if err != nil || !res.Reachable || res.Method != stationsync.MethodRsync {
		t.Fatal(res, err)
	}
	down := vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "down:/vault/"}
	res, err = CheckTarget(ctx, w.env, w.p, vaultrpc.TargetCheckParams{Target: &down})
	if err != nil || res.Reachable || res.FailureCause != stationsync.CauseConnectRefused || res.Reason == "" {
		t.Fatal(res, err)
	}
	// Checking writes nothing: the location was never saved.
	if _, err := os.Stat(w.p.Targets()); !os.IsNotExist(err) {
		t.Fatal("check saved the target")
	}
	if err := SetTarget(w.p, down); err != nil {
		t.Fatal(err)
	}
	if res, err = CheckTarget(ctx, w.env, w.p, vaultrpc.TargetCheckParams{Name: "nas"}); err != nil || res.Reachable {
		t.Fatal(res, err)
	}
	for _, p := range []vaultrpc.TargetCheckParams{{}, {Name: "nas", Target: &up}} {
		if _, err := CheckTarget(ctx, w.env, w.p, p); err == nil {
			t.Fatal("ambiguous params accepted")
		}
	}
	managed := vaultrpc.Target{Name: "home", Kind: "rsync", Dest: "10.0.0.2:/v", Station: &vaultrpc.StationConn{Address: "10.0.0.2", Port: 22, User: "v", KnownHosts: []string{"10.0.0.2 ssh-ed25519 AAAA"}}}
	if _, err := CheckTarget(ctx, w.env, w.p, vaultrpc.TargetCheckParams{Target: &managed}); err == nil {
		t.Fatal("managed location checked")
	} else if ue, ok := valise.As(err); !ok || ue.Code != "check_not_applicable" {
		t.Fatal(err)
	}
}
