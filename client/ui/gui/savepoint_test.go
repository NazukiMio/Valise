package gui

import (
	"context"
	"os/exec"
	"testing"

	"github.com/egoist/mygo/ui"

	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

func TestJourneySavepointFilterAndPromotion(t *testing.T) {
	w := newNativeFlowWorld(t)
	a, s := w.a, w.a.svc
	a.run = func(f func()) { f() }
	a.update = func(f func()) { f() }
	s.OwnerAuth.Passphrase = func(string) (string, error) { return w.pass, nil }
	ctx := context.Background()
	if err := s.ServerNameSet(ctx, "vps", "vps"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Seal(ctx, "vps", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul, Note: "手动备注"}); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(s.Config.SSHBinary, "vps", "$HOME/.local/bin/valise-porter seal-timer --instance demo").CombinedOutput(); err != nil {
		t.Fatalf("fake timer: %s %v", out, err)
	}
	a.scanTimeline = s.ScanTimelineInScope
	a.timelineScope = service.TimelineScanRequest{ConnectionIDs: []string{"vps"}}
	a.readTimeline(nil)
	row := &agentRow{Server: "vps", Instance: "demo", Name: "Demo"}
	tt := ui.NewTester(func(c *ui.Context) { a.journey(c, row, a.timeline.value.Items) }, 1180, 900)
	tt.Frame()
	hasAll(t, tt.Texts(), "自动快照", "手动存档点", "手动备注")
	a.journeyFilter = 1
	tt.Frame()
	if tt.HasText("自动快照") {
		t.Fatal("savepoint filter still displays an automatic point")
	}
	a.journeyFilter = 0
	for _, p := range a.timeline.value.Items {
		if p.Kind == vault.KindAuto {
			a.point = pointKey(p)
		}
	}
	if a.point == "" {
		t.Fatal("automatic time point is missing")
	}
	tt.Frame()
	click(t, tt, "钉为手动存档点…")
	if a.pointPromotion == nil {
		t.Fatal("promotion form did not open")
	}
	a.pointPromotion.note = "升级前留档"
	tt.Frame()
	click(t, tt, "钉为存档点")
	if st := a.pointPromotion; st == nil || !st.published || st.job.err != nil {
		t.Fatalf("promotion did not complete: %+v", st)
	}
	tt.Frame()
	hasAll(t, tt.Texts(), "手动存档点已保存。", "升级前留档")
	for _, p := range a.timeline.value.Items {
		if p.Kind != vault.KindSavepoint {
			t.Fatalf("promotion left a duplicate automatic point: %+v", p)
		}
	}
}
