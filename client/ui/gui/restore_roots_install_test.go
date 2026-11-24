package gui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/client/minisign/minisigntest"
	"valise/client/porter"
	"valise/core/action"
	"valise/core/agentkit"
	"valise/core/install"
	"valise/core/journey"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
)

func serviceReleaseWithHTTP(hc *http.Client) porter.Release {
	return porter.Release{HTTP: hc}
}

func testTimelinePoint() journey.TimelinePoint {
	return journey.TimelinePoint{
		SnapshotRef:        journey.SnapshotRef{VaultID: "v1", SnapshotID: "snap1"},
		CreatedAt:          time.Now(),
		RecordedInstanceID: "demo",
		Scope:              journey.Scope{Tier: "full"},
		Copies: []journey.Copy{{
			LocationRef:   journey.LocationRef{Kind: "server_vault", ID: "box"},
			SourceLocator: "box/demo/2026-11",
			HeaderDigest:  "hdr",
		}},
	}
}

type (
	revivePrevList = revive.PrevListResult
	reviveOldList  = revive.OldListResult
)

func profileActionRun(exit int, out string) action.RunResult {
	return action.RunResult{Exit: exit, Output: out}
}

func TestRestoreRootsStructuredPickerNoRawSyntax(t *testing.T) {
	a, _, r := restoreWorld(t)
	tt := ui.NewTester(a.View, 1180, 1400)
	a.agents.value.Items[0].Instance.ProfileID = "openclaw"
	r.sources[0].Copy.Metadata.ProfileID = "openclaw"

	var reqs []service.TargetPlacementRequest
	a.inspectPlacement = func(_ context.Context, req service.TargetPlacementRequest) (*service.TargetPlacement, error) {
		reqs = append(reqs, req)
		return &service.TargetPlacement{
			TargetHome:          "/home/target",
			DefaultStateDir:     "/home/target/.openclaw",
			RecommendedStateDir: "/home/target/.openclaw-xiaoman",
			SourceRoots:         map[string]string{"state": "/home/me/.openclaw"},
			ExtraRootIDs:        []string{"workspace"},
			DefaultOccupiedBy:   []string{"existing-agent"},
			CanReplaceDefault:   true,
			Relocatable:         true,
		}, nil
	}

	click(t, tt, T("restore.mode.migrate"))
	r.target = "cloud"
	tt.Frame()

	if len(reqs) != 1 || reqs[0].Target != "cloud" || reqs[0].ProfileID != "openclaw" {
		t.Fatalf("placement requests = %+v", reqs)
	}
	for _, x := range tt.Texts() {
		if strings.Contains(x, "state=") || strings.Contains(x, "--root") {
			t.Fatalf("raw state= or --root syntax shown to user: %q", x)
		}
	}
	hasAll(t, tt.Texts(),
		T("restore.place.recommended"),
		T("restore.place.source_dir", "dir", "/home/me/.openclaw"),
		T("restore.place.replace_default", "dir", "/home/target/.openclaw"),
		T("restore.place.extra_roots"),
	)
	if r.resolvedRoots() != "state=/home/target/.openclaw-xiaoman" {
		t.Fatalf("resolvedRoots = %q", r.resolvedRoots())
	}

	// Choosing replace default shows occupied warning and resolves to DefaultStateDir.
	click(t, tt, T("restore.place.replace_default", "dir", "/home/target/.openclaw"))
	hasAll(t, tt.Texts(), T("restore.place.occupied_warn", "instances", "existing-agent"))
	if r.resolvedRoots() != "state=/home/target/.openclaw" {
		t.Fatalf("resolvedRoots replace = %q", r.resolvedRoots())
	}

	// Extra root appends cleanly without requiring state= syntax from user.
	r.placeMode = "recommended"
	r.extraRoots["workspace"] = "/home/target/ws"
	if got := r.resolvedRoots(); got != "state=/home/target/.openclaw-xiaoman\nworkspace=/home/target/ws" {
		t.Fatalf("resolvedRoots with extra = %q", got)
	}
}

func TestSavedInstallsCopySentenceAndInlineMissingBodyInstall(t *testing.T) {
	warnScript := []byte("#!/bin/sh\necho fetch https://cdn.example.invalid/pkg.tgz\n")
	sum := sha256.Sum256(warnScript)
	shaHex := hex.EncodeToString(sum[:])

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(warnScript)
	}))
	defer ts.Close()

	svc := &service.Service{
		Dirs:    config.Dirs{Data: t.TempDir(), Cache: t.TempDir()},
		Release: serviceReleaseWithHTTP(ts.Client()),
	}
	a := New(svc)
	a.run = func(f func()) { f() }
	var copied []string
	a.SetClipboard(func(s string) error { copied = append(copied, s); return nil })
	tt := ui.NewTester(a.View, 1180, 1400)

	// Open custom install, plan risky script -> copy sentence button works + save preset checkbox saves preset.
	a.router.Push("/servers/box/install-custom")
	a.customInstalls["box"] = &customInstallRun{raw: ts.URL + "/install.sh"}
	tt.Advance(time.Second)
	click(t, tt, T("install.custom.prepare"))
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), shaHex, T("install.custom.copy_sentence"), T("install.custom.save_preset"))

	click(t, tt, T("install.custom.copy_sentence"))
	if len(copied) != 1 || copied[0] != install.RiskSentence {
		t.Fatalf("copied = %v", copied)
	}
	hasAll(t, tt.Texts(), T("install.custom.copied_sentence"))

	// Save preset directly via save_now (after checking save_preset).
	click(t, tt, T("install.custom.save_preset"))
	a.customInstalls["box"].presetName = "官方脚本方案"
	click(t, tt, T("install.custom.save_now"))
	tt.Advance(time.Second)

	// On-server page lists the saved preset badged "用户自存" and clicking it triggers PlanCustomInstall.
	a.scans["box"] = &serverScan{adopts: map[string]*adoption{}}
	a.router.Push("/servers/box/agents")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "官方脚本方案", T("install.saved.badge"), T("install.saved.delete"))

	click(t, tt, "官方脚本方案")
	tt.Advance(time.Second)
	if a.router.Path() != "/servers/box/install-custom" {
		t.Fatalf("saved preset routed to %q", a.router.Path())
	}
	ci := a.customInstalls["box"]
	if ci == nil || ci.plan == nil || ci.plan.SHA256 != shaHex || !ci.plan.NeedsConfirm {
		t.Fatalf("saved preset bypassed PlanCustomInstall verification: %+v", ci)
	}

	// Inline custom install inside restore body_rebuild_unverified also uses saved presets and runs PlanCustomInstall.
	a.openRestore(testTimelinePoint(), "box")
	r := a.restore
	r.step = "env"
	r.envNeeded = true
	r.plan = &service.PreparedRestore{Revive: &service.RevivePlan{
		Target: "cloud", From: "box",
		Plan:     revive.Plan{Mode: revive.ModeMigrate, InstanceID: "demo", Tier: "full", Profile: profile.Profile{ID: "demo"}, Body: revive.BodyPlan{Action: revive.BodyKeep}},
		Blockers: []valise.UserError{{Code: "body_rebuild_unverified", Reason: "缺少程序身体"}},
	}}
	tt.Frame()
	hasAll(t, tt.Texts(), T("restore.inline_install.title"), T("restore.inline_install.presets"), "官方脚本方案")
	click(t, tt, "官方脚本方案")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), shaHex, T("install.custom.copy_sentence"))

	// Delete saved preset removes it from store.
	a.router.Push("/servers/box/agents")
	tt.Advance(time.Second)
	click(t, tt, T("install.saved.delete"))
	tt.Advance(time.Second)
	if tt.HasText("官方脚本方案") {
		t.Fatal("deleted saved preset still rendered")
	}
}

func TestStaleOverrideIdentityCategoryAndSelectable(t *testing.T) {
	a, _ := settingsWorld(t)
	signer := minisigntest.New()
	lines := strings.Split(strings.TrimSpace(signer.PublicKey), "\n")
	a.svc.Release.PublicKey = lines[len(lines)-1]

	pack := writeSignedPack(t, signedProfileYAML, "", "2026.11.21", signer)
	pf, err := a.svc.ReadPackFile(pack)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.svc.InstallPack(pf); err != nil {
		t.Fatal(err)
	}
	// Import local YAML with version <= official (v2 <= v2) -> LocalStale == true.
	localPath := filepath.Join(t.TempDir(), "demo.yaml")
	if err := os.WriteFile(localPath, []byte(signedProfileYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	lf, err := a.svc.ReadLocalProfileFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.svc.InstallLocalProfile(lf); err != nil {
		t.Fatal(err)
	}

	tt := ui.NewTester(a.View, 1180, 1400)
	a.router.Push("/settings")
	tt.Advance(time.Second)
	click(t, tt, T("settings.tab.profiles"))
	hasAll(t, tt.Texts(), T("profiles.override_stale_item", "id", "demo", "local", 2, "official", 2), T("profiles.restore_official"))

	// Health proposal with category "identity" shows category.identity label.
	a.health["home/xiaoman"] = &healthState{at: time.Now(), report: &service.HealthReport{}}
	a.proposalsHook = &proposalsOps{
		list: func(context.Context, string, string) ([]agentkit.Proposal, error) {
			return []agentkit.Proposal{{
				ID:      "p-id",
				Kind:    agentkit.ProposalKindClassify,
				Rule:    profile.Override{Root: "state", Glob: "SOUL.md", Category: "identity"},
				Reason:  "core persona rule",
				Created: time.Now(),
			}}, nil
		},
	}
	a.agentTab = tabHealth
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "state:SOUL.md -> identity", T("category.identity"), "core persona rule")

	// Selectable check: right-clicking errorBox reason & notice text opens text selection context menu.
	errTester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Gap(12).Padding(20).Children(func() {
			errorBox(c, valise.New("err_sel", "可选中错误原因", "可选中建议"), new(bool))
			notice(c, "warn", "可选中提醒文字")
			statusRow(c, "ok", "状态标签", "可选中状态值")
			fact(c, "字段标签", "可选中字段值")
		})
	}, 800, 600)
	for _, txt := range []string{"可选中错误原因", "可选中建议", "可选中提醒文字", "可选中状态值", "可选中字段值"} {
		if err := errTester.RightClick(txt); err != nil {
			t.Fatalf("RightClick(%q): %v", txt, err)
		}
		if menu := errTester.Menu(); len(menu) == 0 {
			t.Fatalf("Selectable context menu missing for %q", txt)
		}
		errTester.CloseMenu()
	}
}
