package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/minisign/minisigntest"
	"valise/core/profile"
	"valise/core/valise"
)

const repoProfiles = "../../profiles"

func buildPack(t *testing.T) []byte {
	t.Helper()
	b, err := Build(repoProfiles, "../../kit/tools.yaml", "2026.10.31", time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func code(err error) string {
	if ue, ok := valise.As(err); ok {
		return ue.Code
	}
	return ""
}

func TestNoPack(t *testing.T) {
	s, err := Load(t.TempDir(), "")
	if err != nil || len(s.ByID) != 0 || s.Pack != nil || s.PackProblem != nil {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestSignedPack(t *testing.T) {
	signer := minisigntest.New()
	data := t.TempDir()
	tgz := buildPack(t)
	if again := buildPack(t); string(again) != string(tgz) {
		t.Fatal("Build is not deterministic")
	}
	sig := signer.Sign(tgz, "file:"+PackAsset)
	if _, err := Install(data, tgz, signer.Sign(append([]byte{}, tgz[:len(tgz)-1]...), "x"), signer.PublicKey); code(err) != "pack_signature_invalid" {
		t.Fatalf("bad signature: %v", err)
	}
	if _, err := Install(data, tgz, nil, signer.PublicKey); code(err) != "pack_unsigned" {
		t.Fatalf("no signature: %v", err)
	}
	p, err := Install(data, tgz, sig, signer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Manifest.Profiles) < 3 {
		t.Fatalf("%+v", p.Manifest)
	}
	if p.Kit == nil || p.Kit.ByID("linger") == nil || p.Manifest.KitTools == nil {
		t.Fatalf("the Kit tool list is missing from the pack: %+v", p.Manifest)
	}
	s, err := Load(data, signer.PublicKey)
	if err != nil || s.PackProblem != nil || s.Pack == nil || s.Pack.Version != "2026.10.31" {
		t.Fatalf("%+v %v", s, err)
	}
	for id, l := range s.ByID {
		if l.Local || l.Effective == nil {
			t.Errorf("%s: %+v", id, l)
		}
	}
	// A different key (or none) makes the installed pack unusable.
	s, _ = Load(data, minisigntest.New().PublicKey)
	if len(s.ByID) != 0 || code(s.PackProblem) != "pack_signature_invalid" {
		t.Fatalf("%+v", s)
	}
	s, _ = Load(data, "")
	if len(s.ByID) != 0 || code(s.PackProblem) != "pack_key_missing" {
		t.Fatalf("%+v", s)
	}
	// Tampering on disk is caught on load.
	os.WriteFile(filepath.Join(PackDir(data), PackAsset), append(tgz, 0), 0o600)
	if s, _ = Load(data, signer.PublicKey); s.PackProblem == nil {
		t.Fatal("tampered pack loaded")
	}
}

func TestReadRejects(t *testing.T) {
	if _, err := Read([]byte("not gzip")); code(err) != "pack_invalid" {
		t.Fatal(err)
	}
	dir := t.TempDir()
	b, _ := os.ReadFile(filepath.Join(repoProfiles, "dsh.yaml"))
	os.WriteFile(filepath.Join(dir, "dsh.yaml"), b, 0o644)
	os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte(strings.Replace(string(b), "id: dsh", "id: Bad", 1)), 0o644)
	if _, err := Build(dir, "", "x", time.Now()); err == nil {
		t.Fatal("Build accepted a profile failing lint")
	}
}

func TestUnsignedPackAsLocal(t *testing.T) {
	data := t.TempDir()
	p, err := Read(buildPack(t))
	if err != nil {
		t.Fatal(err)
	}
	cmds := 0
	for _, d := range p.Docs {
		cmds += len(Commands(d.Profile))
	}
	if cmds == 0 {
		t.Fatal("no commands listed for review")
	}
	paths, err := InstallLocal(data, p)
	if err != nil || len(paths) != len(p.Docs) {
		t.Fatal(paths, err)
	}
	s, err := Load(data, "")
	if err != nil || len(s.ByID) != len(p.Docs) {
		t.Fatalf("%+v %v", s, err)
	}
	for _, l := range s.ByID {
		if !l.Local {
			t.Fatal("unsigned profiles must be local")
		}
	}
}

func TestLocalProfiles(t *testing.T) {
	data := t.TempDir()
	dir := LocalDir(data)
	os.MkdirAll(dir, 0o755)
	good := `schema: valise-profile/1
id: my-local
display_name: Mine
profile_version: 1
status: draft
detect: { binaries: [mine], state_dir_candidates: ["${HOME}/.mine"] }
paths: [ { root: state, glob: "**", category: memory, verify: pending } ]
provenance: { kind: binary, binary: { name: mine } }
`
	os.WriteFile(filepath.Join(dir, "mine.yaml"), []byte(good), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(strings.Replace(good, "id: my-local", "id: Bad", 1)), 0o644)
	s, err := Load(data, "")
	if err != nil {
		t.Fatal(err)
	}
	if l := s.ByID["my-local"]; l == nil || !l.Local {
		t.Fatalf("%+v", s.ByID)
	}
	if len(s.Rejected) != 1 {
		t.Fatalf("rejected %v", s.Rejected)
	}
}

func TestCommandsFormat(t *testing.T) {
	p := &profile.Profile{
		Detect: &profile.Detect{VersionArgv: profile.Argv{"x", "--version"}},
		Install: &profile.Install{Steps: []profile.InstallStep{
			{Kind: profile.StepScript, URL: "https://e.invalid/i.sh", SHA256: "0123456789abcdef", Interpreter: profile.Argv{"bash"}},
			{Kind: profile.StepPathAdd, Dir: "${HOME}/.local/bin"},
		}},
	}
	got := Commands(p)
	want := []string{
		"检测版本：x --version",
		"安装脚本：https://e.invalid/i.sh（SHA-256 0123456789ab）-> bash <downloaded:i.sh>",
		"加入 PATH：${HOME}/.local/bin",
	}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %q != %q", i, got[i], want[i])
		}
	}
}

func TestWriteLocalFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteLocalFile(dir, "../bad", []byte("x")); err == nil {
		t.Error("bad id accepted")
	}
	p, err := WriteLocalFile(dir, "my-profile", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("%v %v", err, st)
	}
}
