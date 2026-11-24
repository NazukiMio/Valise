package gui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/minisign/minisigntest"
	"valise/client/profiles"
	"valise/core/enroll"
	"valise/ui/service"
)

const signedProfileYAML = `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 2
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths: [{ root: state, glob: "demo.json", category: config, verify: pending }]
provenance: { kind: binary, binary: { name: demo } }
native_cli: [/bin/echo, onboard]
native_session_cli: [/bin/echo, tui]
`

const unverifiedProfileYAML = `schema: valise-profile/1
id: extra
display_name: Extra
profile_version: 1
status: draft
detect: { binaries: [extra], version_argv: [extra, --version], state_dir_candidates: ["${HOME}/.extra"], markers_any: [extra.json] }
paths: [{ root: state, glob: "extra.json", category: config, verify: pending }]
provenance: { kind: binary, binary: { name: extra } }
`

const kitToolsTestYAML = `schema: valise-kit-tools/1
tools:
  - id: rclone
    tier: enhanced
    name: rclone
    purpose: 同步到网盘
    without: 不能同步到网盘
    check: { binary: rclone }
    binary:
      version: "1.75.2"
      archives:
        - { arch: amd64, url: "https://downloads.rclone.org/v1.75.2/rclone-v1.75.2-linux-amd64.zip", sha256: "349ac8fba6ff65d6247043f1750cdcb518ec5d500ef91463a10d37c0ccdf3702", member: "rclone-v1.75.2-linux-amd64/rclone" }
    clouds:
      - id: gdrive
        name: Google Drive
        rclone_type: drive
        access: folder
        access_note: 只看自己建的文件夹
      - id: webdav
        name: WebDAV
        rclone_type: webdav
        browser: false
        access: account
        access_note: 整个目录树
        verify: pending
`

func writeSignedPack(t *testing.T, yamlBody, kitBody, packVer string, s *minisigntest.Signer) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "p.yaml"), []byte(yamlBody), 0o600); err != nil {
		t.Fatal(err)
	}
	kitPath := ""
	if kitBody != "" {
		kitPath = filepath.Join(t.TempDir(), "kit-tools.yaml")
		if err := os.WriteFile(kitPath, []byte(kitBody), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tgz, err := profiles.Build(src, kitPath, packVer, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	packPath := filepath.Join(outDir, profiles.PackAsset)
	if err := os.WriteFile(packPath, tgz, 0o600); err != nil {
		t.Fatal(err)
	}
	if s != nil {
		if err := os.WriteFile(packPath+".minisig", s.Sign(tgz, "file:"+profiles.PackAsset), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return packPath
}

func TestProfilesSettingsInstallVerifiedAndUnverifiedLocal(t *testing.T) {
	a, tt := settingsWorld(t)
	signer := minisigntest.New()
	lines := strings.Split(strings.TrimSpace(signer.PublicKey), "\n")
	a.svc.Release.PublicKey = lines[len(lines)-1]

	verifiedPack := writeSignedPack(t, signedProfileYAML, kitToolsTestYAML, "2026.11.19", signer)
	untrustedPack := writeSignedPack(t, unverifiedProfileYAML, "", "2026.11.20", minisigntest.New())

	picked := verifiedPack
	a.SetFilePicker(func(title, msg string) (string, error) { return picked, nil })

	a.router.Push("/settings")
	tt.Frame()
	click(t, tt, T("settings.tab.profiles"))
	hasAll(t, tt.Texts(), T("profiles.title"), T("profiles.none"), T("profiles.check_online"), T("profiles.pick_file"))

	// Preview and install verified pack: shows clouds list and reloads harness live.
	click(t, tt, T("profiles.pick_file"))
	wantClouds := T("profiles.clouds_list", "list", "Google Drive"+T("common.dot_sep")+"WebDAV")
	hasAll(t, tt.Texts(), T("profiles.preview_title", "version", "2026.11.19"), "demo v2", wantClouds, T("profiles.sig_ok"), T("profiles.install_go"))
	if tt.HasText(T("profiles.install_local")) {
		t.Fatalf("verified pack offered unverified local import: %q", tt.Texts())
	}
	click(t, tt, T("profiles.install_go"))
	hasAll(t, tt.Texts(), T("profiles.installed", "version", "2026.11.19"), T("profiles.install_done", "version", "2026.11.19"), "demo v2", wantClouds)
	if a.harness["demo"] != "Demo" || !a.hasSessionCLI["demo"] {
		t.Fatalf("profiles did not reload live: harness=%v session=%v", a.harness, a.hasSessionCLI)
	}

	// Pick untrusted pack: cannot install as official pack, shows commands & allows local import.
	picked = untrustedPack
	click(t, tt, T("profiles.pick_file"))
	hasAll(t, tt.Texts(), T("profiles.preview_title", "version", "2026.11.20"), T("profiles.unverified_note"), T("profiles.unverified_cmds"), T("profiles.install_local"))
	if tt.HasText(T("profiles.install_go")) {
		t.Fatalf("untrusted pack allowed verified install: %q", tt.Texts())
	}
	click(t, tt, T("profiles.unverified_cmds"))
	hasAll(t, tt.Texts(), "[extra] 检测版本：extra --version")
	click(t, tt, T("profiles.install_local"))
	hasAll(t, tt.Texts(), T("profiles.local_done", "n", 1), T("profiles.local_list", "list", "extra"))
	if a.harness["extra"] != "Extra" {
		t.Fatalf("local profile did not reload live: %v", a.harness)
	}

	// Corrupt pack reports profiles.problem cleanly.
	if err := os.WriteFile(filepath.Join(profiles.PackDir(a.svc.Dirs.Data), profiles.PackAsset), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	click(t, tt, T("common.refresh"))
	if !tt.HasText("当前资料包不可用") {
		t.Fatalf("broken pack state missing: %q", tt.Texts())
	}
}

func TestProfilesSettingsLocalYAMLImport(t *testing.T) {
	a, tt := settingsWorld(t)
	signer := minisigntest.New()
	lines := strings.Split(strings.TrimSpace(signer.PublicKey), "\n")
	a.svc.Release.PublicKey = lines[len(lines)-1]
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	extra := write("extra.yaml", unverifiedProfileYAML)
	picked := extra
	a.SetFilePicker(func(title, msg string) (string, error) { return picked, nil })

	a.router.Push("/settings")
	tt.Frame()
	click(t, tt, T("settings.tab.profiles"))
	hasAll(t, tt.Texts(), T("profiles.pick_yaml"))

	click(t, tt, T("profiles.pick_yaml"))
	hasAll(t, tt.Texts(), T("profiles.local_preview_title", "name", "Extra", "id", "extra"), T("profiles.local_unverified_note"), T("profiles.local_import_go"))
	if tt.HasText(T("profiles.local_replaces")) {
		t.Fatalf("fresh import showed overwrite warning: %q", tt.Texts())
	}
	click(t, tt, T("profiles.local_import_go"))
	hasAll(t, tt.Texts(), T("profiles.local_import_done", "id", "extra"), T("profiles.local_list", "list", "extra"))
	if a.harness["extra"] != "Extra" {
		t.Fatalf("local profile did not reload live: %v", a.harness)
	}

	// Same id again: overwrite warning.
	click(t, tt, T("profiles.pick_yaml"))
	hasAll(t, tt.Texts(), T("profiles.local_replaces"))
	click(t, tt, T("common.cancel"))

	// Official pack with the same id allows local override with diff preview, post-import instance sync banner, and one-click restore to official.
	pack := writeSignedPack(t, signedProfileYAML, "", "2026.11.19", signer)
	picked = pack
	click(t, tt, T("profiles.pick_file"))
	click(t, tt, T("profiles.install_go"))

	a.agents.value.Items = []service.AgentSummary{
		{ConnectionID: "home", Instance: enroll.InstanceSummary{InstanceID: "demo1", ProfileID: "demo", ProfileVersion: 2}},
	}
	overrideYAML := strings.Replace(signedProfileYAML, "profile_version: 2", "profile_version: 3", 1) +
		"entries:\n  - { id: web, kind: web, host: 127.0.0.1, port: 8080, path: \"/\" }\n"
	picked = write("demo.yaml", overrideYAML)
	click(t, tt, T("profiles.pick_yaml"))
	hasAll(t, tt.Texts(),
		T("profiles.local_overrides_official", "id", "demo", "version", 2),
		T("profiles.diff_title"),
		"版本：v2 → v3",
		"新增入口：web",
		T("profiles.local_import_go"),
	)
	click(t, tt, T("profiles.local_import_go"))
	hasAll(t, tt.Texts(),
		T("profiles.override_item", "id", "demo", "local", 3, "official", 2),
		T("profiles.restore_official"),
		T("profiles.sync_banner", "n", 1),
		T("profiles.sync_now", "n", 1),
	)

	// One-click restore back to official removes the local override.
	click(t, tt, T("profiles.restore_official"))
	hasAll(t, tt.Texts(), T("profiles.restore_official_done", "id", "demo"))
	if tt.HasText(T("profiles.override_item", "id", "demo", "local", 3, "official", 2)) {
		t.Fatalf("override still listed after restoring official: %q", tt.Texts())
	}
}

func TestSettingsProfilesRestoreAllOfficial(t *testing.T) {
	a, tt := settingsWorld(t)
	signer := minisigntest.New()
	lines := strings.Split(strings.TrimSpace(signer.PublicKey), "\n")
	a.svc.Release.PublicKey = lines[len(lines)-1]

	// Build a signed official pack with two profiles ("demo" and "extra").
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "demo.yaml"), []byte(signedProfileYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extra.yaml"), []byte(unverifiedProfileYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	tgz, err := profiles.Build(src, "", "2026.11.24", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	packPath := filepath.Join(t.TempDir(), profiles.PackAsset)
	if err := os.WriteFile(packPath, tgz, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packPath+".minisig", signer.Sign(tgz, "file:"+profiles.PackAsset), 0o600); err != nil {
		t.Fatal(err)
	}
	pf, err := a.svc.ReadPackFile(packPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.svc.InstallPack(pf); err != nil {
		t.Fatal(err)
	}

	// Import local overrides for both profiles.
	for name, body := range map[string]string{"demo.yaml": signedProfileYAML, "extra.yaml": unverifiedProfileYAML} {
		lp := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(lp, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		lf, err := a.svc.ReadLocalProfileFile(lp)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.svc.InstallLocalProfile(lf); err != nil {
			t.Fatal(err)
		}
	}

	a.router.Push("/settings")
	tt.Frame()
	click(t, tt, T("settings.tab.profiles"))
	hasAll(t, tt.Texts(),
		T("profiles.override_banner", "n", 2),
		T("profiles.restore_all_official"),
	)

	click(t, tt, T("profiles.restore_all_official"))
	hasAll(t, tt.Texts(), T("profiles.restore_all_done", "n", 2))
	if tt.HasText(T("profiles.restore_all_official")) || tt.HasText(T("profiles.override_banner", "n", 2)) {
		t.Fatalf("overrides still listed after restore all: %q", tt.Texts())
	}
}

func TestReloadProfilesInvalidatesServiceAndProfilesCaches(t *testing.T) {
	a, _ := world(t)
	st := &agentServiceState{started: true}
	a.agentServices["cloud/alan"] = st
	a.profiles = &profilesState{started: true}

	a.reloadProfiles()

	if st.started {
		t.Fatal("reloadProfiles did not reset cached agentServiceState.started")
	}
	if a.profiles.started {
		t.Fatal("reloadProfiles did not reset cached profilesState.started")
	}

	// Direct readAgentService immediately re-marks started=true.
	a.agentServiceHook = &agentServiceOps{
		status: func(context.Context, string, string) (*service.InstanceServiceInfo, error) {
			return &service.InstanceServiceInfo{}, nil
		},
		actions: func(context.Context, string, string) (*service.ActionList, error) { return &service.ActionList{}, nil },
	}
	a.readAgentService("cloud", "alan")
	if !st.started {
		t.Fatal("readAgentService did not set st.started = true")
	}
}
