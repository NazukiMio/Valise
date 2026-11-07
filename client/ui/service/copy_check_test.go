package service

import (
	"strings"
	"testing"
	"time"

	"valise/core/protection"
	"valise/core/rpc"
	"valise/core/stationsync"
	"valise/core/vaultrpc"
)

func TestCopyHealthItems(t *testing.T) {
	r := &HealthReport{}
	nas := protection.LocationRef{Kind: "legacy_target", ID: "nas"}
	copyItems(r, []protection.CopyStatus{
		{TargetRef: nas, At: time.Now(), State: "failed", FailureCause: stationsync.CauseConnectTimeout},
		{TargetRef: protection.LocationRef{ID: "ok"}, At: time.Now(), State: "succeeded"},
	})
	if len(r.Items) != 1 || r.Items[0].Level != HealthWarn || r.Items[0].ID != "copy-nas" ||
		!strings.Contains(r.Items[0].Text, "NAT") || !strings.Contains(r.Items[0].Detail, "Tailscale") {
		t.Fatalf("%+v", r.Items)
	}
	for _, c := range []string{stationsync.CauseHostUnknown, stationsync.CauseAuthDenied, stationsync.CauseHostKeyUnverified,
		stationsync.CauseNotWritable, stationsync.CauseToolMissing, stationsync.CauseOther, ""} {
		what, remedy := CopyCause(c, "nas")
		if what == "" || remedy == "" || strings.Contains(what+remedy, "空间站") {
			t.Fatalf("%s: %q %q", c, what, remedy)
		}
	}
}

func TestTargetCheckNeedsCapability(t *testing.T) {
	wantCode(t, n2Compatibility(&rpc.HelloResult{}, vaultrpc.MethodTargetCheck, nil), "unsupported_capability")
	if err := n2Compatibility(&rpc.HelloResult{Capabilities: []string{vaultrpc.CopyCheckCapability}}, vaultrpc.MethodTargetCheck, nil); err != nil {
		t.Fatal(err)
	}
}
