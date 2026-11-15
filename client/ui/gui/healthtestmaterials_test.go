package gui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/journey"
	"valise/ui/service"
)

func TestTestMaterialReviewShowsSourceAndKeepsFooterInSmallWindow(t *testing.T) {
	a, _ := healthRestoreWorld(t)
	reply := make(chan bool, 1)
	baseTime := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	location := journey.LocationRef{Kind: "server_vault", ID: "home"}
	flow := &restoreTestFlow{reviewReply: reply, materialReview: &service.TestMaterialReview{TargetTime: baseTime.Add(24 * time.Hour), TargetTier: "soul", Files: 15, Bytes: 1200, Partial: true, BaselineCredentials: true,
		Baseline: &environment.DataOrigin{Time: baseTime, Snapshot: identity.SnapshotRef{VaultID: "vault", SnapshotID: "base"}}, BaselineLocation: &location}}
	a.restoreTest = flow
	flow.server, flow.instance = "home", "xiaoman"
	flow.j.busy = true
	tt := ui.NewTester(a.View, 860, 560)
	hasAll(t, tt.Texts(), T("restoretest.materials.title"), baseTime.Local().Format("2006-01-02 15:04:05"), T("restoretest.materials.partial"), T("restoretest.materials.explain"))
	shot(t, tt, "restore-test-materials-small")
	click(t, tt, T("restoretest.materials.continue"))
	select {
	case approved := <-reply:
		if !approved {
			t.Fatal("material approval changed")
		}
	default:
		t.Fatal("small-window footer could not be clicked")
	}
}

func TestFullTestSelectionCancelWorksInSmallWindow(t *testing.T) {
	a, _ := healthRestoreWorld(t)
	a.health["home/xiaoman"].canEnvironmentTest = true
	flow := &restoreTestFlow{server: "home", instance: "xiaoman", confirm: true, depth: "isolated"}
	a.restoreTest = flow
	tt := ui.NewTester(a.View, 860, 560)
	shot(t, tt, "restore-test-selection-small")
	click(t, tt, T("common.cancel"))
	if flow.confirm {
		t.Fatal("small-window cancel hidden behind content")
	}
}
