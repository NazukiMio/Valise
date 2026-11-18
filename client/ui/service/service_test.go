package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"filippo.io/age"

	"valise/client/config"
	"valise/client/keystore"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/testbed/fakesshenv"
)

func newService(t *testing.T, sshBinary string) *Service {
	t.Helper()
	dir := t.TempDir()
	s := &Service{
		Dirs:          config.Dirs{Data: filepath.Join(dir, "data"), Cache: filepath.Join(dir, "cache")},
		Config:        config.Config{SSHBinary: sshBinary},
		Version:       "test",
		Home:          dir,
		SSHConfigPath: filepath.Join(dir, "ssh_config"),
	}
	t.Cleanup(s.Close)
	return s
}

func (s *Service) rememberOwner(id age.Identity) {
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	s.cacheOwner(id, "")
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if ue, ok := valise.As(err); !ok || ue.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestDoctorDeployDoctor(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	s := newService(t, e.SSH)
	ctx := context.Background()

	r, err := s.Doctor(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	if r.PorterFound || r.Problem == nil || r.Problem.Code != "porter_not_found" || r.Platform.Arch != runtime.GOARCH {
		t.Fatalf("%+v", r)
	}

	_, err = s.DeployPorter(ctx, "box", DeploySource{})
	wantCode(t, err, "deploy_source_required")
	_, err = s.DeployPorter(ctx, "box", DeploySource{LocalPath: "a", ReleaseTag: "b"})
	wantCode(t, err, "deploy_source_required")
	_, err = s.DeployPorter(ctx, "box", DeploySource{ReleaseTag: "v0.1.0"})
	wantCode(t, err, "release_key_missing")

	dr, err := s.DeployPorter(ctx, "box", DeploySource{LocalPath: e.Porter()})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local/bin/valise-porter")
	if dr.Porter.Path != want {
		t.Fatalf("%+v", dr)
	}

	r, err = s.Doctor(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	if !r.PorterFound || r.Problem != nil || r.PorterPath != want || r.Home != home ||
		r.Protocol != rpc.ProtocolVersion || len(r.Tools) != len(rpc.ToolNames) || r.PendingOps == nil {
		t.Fatalf("%+v", r)
	}
}

func TestDoctorUnreachable(t *testing.T) {
	e := fakesshenv.New(t)
	_, err := newService(t, e.SSH).Doctor(context.Background(), "nohost")
	wantCode(t, err, "ssh_connect_failed")
}

func TestDoctorUsesSSHConfig(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	s := newService(t, e.SSH)
	os.WriteFile(s.SSHConfigPath, []byte("Host box\n  HostName 192.0.2.7\n  User u\n  ControlPath ~/.ssh/cm-%C\n"), 0o600)
	r, err := s.Doctor(context.Background(), "box")
	if err != nil {
		t.Fatal(err)
	}
	if r.SSH.HostName != "192.0.2.7" || r.SSH.User != "u" || !r.ReusesUserControlMaster {
		t.Fatalf("%+v", r)
	}
}

const pass = "a long enough passphrase"

func TestKeys(t *testing.T) {
	s := newService(t, "ssh")
	_, err := s.KeyShow()
	wantCode(t, err, "keystore_missing")
	_, err = s.KeyInit("short")
	wantCode(t, err, "passphrase_too_short")

	info, err := s.KeyInit(pass)
	if err != nil {
		t.Fatal(err)
	}
	if shown, _ := s.KeyShow(); *shown != *info {
		t.Fatalf("%+v != %+v", shown, info)
	}
	_, err = s.KeyInit(pass)
	wantCode(t, err, "keystore_exists")
	_, err = s.Unlock("wrong passphrase!")
	wantCode(t, err, "passphrase_wrong")
	k, err := s.Unlock(pass)
	if err != nil || k.Recipient() != info.Recipient {
		t.Fatal(err)
	}
	k.Lock()

	kit := filepath.Join(t.TempDir(), "kit")
	if _, err := s.ExportKit(kit); err != nil {
		t.Fatal(err)
	}
	other := newService(t, "ssh")
	got, err := other.ImportKit(kit, pass)
	if err != nil || got.Fingerprint != info.Fingerprint {
		t.Fatalf("%+v %v", got, err)
	}
	st, _ := os.Stat(filepath.Join(other.Dirs.Data, keystore.KeystoreFile))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

func TestKeyImport(t *testing.T) {
	s := newService(t, "ssh")
	_, err := s.KeyImport([]byte("nope"), pass)
	wantCode(t, err, "identity_invalid")
	k, _ := keystore.Generate()
	id, _ := k.Identity()
	info, err := s.KeyImport([]byte(id.String()), pass)
	if err != nil || info.Recipient != k.Recipient() {
		t.Fatal(err)
	}
}
