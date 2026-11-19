package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/minisign/minisigntest"
	"valise/client/profiles"
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

func writeSignedPack(t *testing.T, yamlBody, packVer string, s *minisigntest.Signer) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "p.yaml"), []byte(yamlBody), 0o600); err != nil {
		t.Fatal(err)
	}
	tgz, err := profiles.Build(src, "", packVer, time.Now())
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

	verifiedPack := writeSignedPack(t, signedProfileYAML, "2026.11.19", signer)
	untrustedPack := writeSignedPack(t, unverifiedProfileYAML, "2026.11.20", minisigntest.New())

	picked := verifiedPack
	a.SetFilePicker(func(title, msg string) (string, error) { return picked, nil })

	a.router.Push("/settings")
	tt.Frame()
	click(t, tt, T("settings.tab.profiles"))
	hasAll(t, tt.Texts(), T("profiles.title"), T("profiles.none"), T("profiles.check_online"), T("profiles.pick_file"))

	// Preview and install verified pack: reloads harness and hasSessionCLI live.
	click(t, tt, T("profiles.pick_file"))
	hasAll(t, tt.Texts(), T("profiles.preview_title", "version", "2026.11.19"), "demo v2", T("profiles.sig_ok"), T("profiles.install_go"))
	if tt.HasText(T("profiles.install_local")) {
		t.Fatalf("verified pack offered unverified local import: %q", tt.Texts())
	}
	click(t, tt, T("profiles.install_go"))
	hasAll(t, tt.Texts(), T("profiles.installed", "version", "2026.11.19"), T("profiles.install_done", "version", "2026.11.19"), "demo v2")
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
