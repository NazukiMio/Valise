package gui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
	"valise/client/config"
	"valise/ui/service"

	"github.com/egoist/mygo/ui"
)

// shot saves the tester's last frame when VALISE_SHOTS names a folder.
func shot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("VALISE_SHOTS")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, tt.Image())
}

func TestShots(t *testing.T) {
	if os.Getenv("VALISE_SHOTS") == "" {
		t.Skip("VALISE_SHOTS not set")
	}
	a, tt := world(t)
	shot(t, tt, "home")
	a.openAgent("home", "xiaoman")
	tt.Frame()
	tt.Advance(time.Second)
	shot(t, tt, "agent")
	a.agentTab = 1
	tt.Frame()
	tt.Advance(time.Second)
	shot(t, tt, "journey")
	a.point = "sb"
	tt.Frame()
	tt.Advance(time.Second)
	shot(t, tt, "point")
}

// TestLiveShots renders the pages over a real data directory (the drill
// config) when VALISE_SHOTS_CONFIG names it.
func TestLiveShots(t *testing.T) {
	cfg := os.Getenv("VALISE_SHOTS_CONFIG")
	if cfg == "" || os.Getenv("VALISE_SHOTS") == "" {
		t.Skip("VALISE_SHOTS_CONFIG not set")
	}
	svc, err := service.New(config.Dirs{Data: cfg, Cache: filepath.Join(cfg, "cache")}, "shots")
	if err != nil {
		t.Fatal(err)
	}
	a := New(svc)
	a.run = func(f func()) { f() }
	a.Start()
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Advance(time.Second)
	shot(t, tt, "live-home")
	t.Logf("agents %d, points %d, err %v %v", len(a.agents.value.Items), len(a.timeline.value.Items), a.agents.err, a.timeline.err)
	for _, it := range a.agents.value.Items {
		if len(pointsFor(a.timeline.value, it.ConnectionID, it.Instance.InstanceID)) > 3 {
			a.openAgent(it.ConnectionID, it.Instance.InstanceID)
			tt.Frame()
			tt.Advance(time.Second)
			shot(t, tt, "live-agent")
			a.agentTab = 1
			tt.Frame()
			shot(t, tt, "live-journey")
			break
		}
	}
	a.router.Push("/servers")
	tt.Frame()
	tt.Advance(time.Second)
	shot(t, tt, "live-servers")
}
