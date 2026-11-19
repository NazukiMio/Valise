package gui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/kittools"
	"valise/ui/service"
)

// cloudBatchWorld is the cloud page with home and cloud ready and nas
// without rclone, probed through a fake.
func cloudBatchWorld(t *testing.T) (*App, *fakeKit, *cloudAdd) {
	a, _ := locWorld(t)
	f := &fakeKit{rclone: map[string]service.KitTool{
		"home": rcloneTool(true, true), "cloud": rcloneTool(true, false), "nas": rcloneTool(false, false)},
		failLogin: map[string]bool{}}
	a.kitHook = f.ops()
	ca := &cloudAdd{read: true, localOK: true, name: "gdrive", id: "google-drive", appID: "id1", appKey: "s3cret",
		clouds: []kittools.Cloud{{ID: "google-drive", Name: "Google Drive", Access: kittools.AccessFolder, AskApp: true}}}
	a.cloudAdding = ca
	for _, h := range a.hosts() {
		a.probeRclone(ca, h)
	}
	return a, f, ca
}

func TestCloudSecretStaysThenIsWiped(t *testing.T) {
	a, f, ca := cloudBatchWorld(t)
	tt := ui.NewTester(a.View, 1180, 2400)
	a.router.Push(cloudAddRoute)
	tt.Frame()
	ca.hostTools["home"].pick = true
	tt.Frame()
	click(t, tt, T("loc.cloud_login_on", "host", "home"))
	if len(f.logins) != 1 || ca.login == nil || ca.appKey != "s3cret" {
		t.Fatalf("logins %v login %v key %q", f.logins, ca.login, ca.appKey)
	}
	click(t, tt, T("common.back"))
	if ca.login != nil || ca.appKey != "s3cret" || a.cloudAdding != ca {
		t.Fatal("returning from the login lost the key")
	}
	a.router.Push("/locations")
	tt.Frame()
	if ca.appKey != "" || a.cloudAdding != nil {
		t.Fatalf("leaving the page kept the key %q", ca.appKey)
	}

	// Done and closing the window wipe it too.
	ca2 := &cloudAdd{appKey: "k2", saved: []string{"home"}}
	a.cloudAdding = ca2
	a.closeAdding()
	if ca2.appKey != "" || a.cloudAdding != nil {
		t.Fatal("done kept the key")
	}
	ca3 := &cloudAdd{appKey: "k3"}
	a.cloudAdding = ca3
	if !a.RequestClose(func() {}) || ca3.appKey != "" {
		t.Fatal("closing the window kept the key")
	}
}

func TestCloudServerWithoutRcloneNeedsConfirmedInstall(t *testing.T) {
	a, f, ca := cloudBatchWorld(t)
	tt := ui.NewTester(a.View, 1180, 2400)
	a.router.Push(cloudAddRoute)
	tt.Frame()
	hasAll(t, tt.Texts(), T("loc.rc_missing"), T("loc.rc_present"), T("loc.rc_system"), T("loc.rc_prepare"), T("loc.cloud_pick_servers"))
	if ca.hostTools["nas"].ready() || ca.hostTools["nas"].pick {
		t.Fatal("a server without rclone can be picked")
	}
	click(t, tt, T("loc.rc_prepare"))
	hasAll(t, tt.Texts(), "https://example.test/rclone.zip", T("tools.confirm_external"), T("tools.install_go"), T("tools.self_install_shell"))
	_ = tt.Click(T("tools.install_go"))
	if len(f.installed) != 0 {
		t.Fatal("installed before confirming")
	}
	click(t, tt, T("tools.confirm_external"))
	click(t, tt, T("tools.install_go"))
	st := ca.hostTools["nas"]
	if len(f.installed) != 1 || !st.present || !st.ours || !st.pick {
		t.Fatalf("after install: %v %+v", f.installed, st)
	}
}

func TestCloudBatchLoginKeepsFailedHostsPickedForRetry(t *testing.T) {
	a, f, ca := cloudBatchWorld(t)
	f.failLogin["cloud"] = true
	tt := ui.NewTester(a.View, 1180, 2400)
	a.router.Push(cloudAddRoute)
	tt.Frame()
	ca.hostTools["home"].pick = true
	ca.hostTools["cloud"].pick = true
	tt.Frame()
	click(t, tt, T("loc.cloud_login_batch", "n", 2))
	if len(f.logins) != 2 || f.logins[0] != "cloud" || f.logins[1] != "home" {
		t.Fatalf("order %v (hosts %v)", f.logins, a.hosts())
	}
	hasAll(t, tt.Texts(), T("loc.cloud_batch_ok", "host", "home"), T("loc.cloud_batch_fail", "host", "cloud", "reason", "fixture login failed"))
	if ca.hostTools["home"].pick || !ca.hostTools["cloud"].pick || len(ca.saved) != 1 || ca.saved[0] != "home" {
		t.Fatalf("pick home=%v cloud=%v saved=%v", ca.hostTools["home"].pick, ca.hostTools["cloud"].pick, ca.saved)
	}
	click(t, tt, T("common.back"))
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("loc.cloud_login_on", "host", "cloud"), T("loc.rc_done"))
	f.failLogin["cloud"] = false
	click(t, tt, T("loc.cloud_login_on", "host", "cloud"))
	if len(f.logins) != 3 || ca.appKey != "s3cret" || len(ca.saved) != 2 {
		t.Fatalf("retry %v key %q saved %v", f.logins, ca.appKey, ca.saved)
	}
}
