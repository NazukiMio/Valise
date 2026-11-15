package login

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
)

func TestLoginLockGuest(t *testing.T) {
	if os.Getenv("VALISE_LOGIN_LOCK_GUEST") != "1" {
		return
	}
	inherited := os.NewFile(3, "inherited-lock")
	if _, err := inherited.Stat(); err != nil {
		os.Exit(11)
	}
	other, err := os.OpenFile(os.Getenv("VALISE_LOGIN_LOCK_PATH"), os.O_RDWR, 0600)
	if err != nil {
		os.Exit(12)
	}
	if syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		os.Exit(13)
	}
	os.Exit(7)
}

func TestLoginRunRetainsAndInheritsMutationLock(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "lock")
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e := &porterenroll.Env{Home: home, Vars: []string{"VALISE_LOGIN_LOCK_GUEST=1", "VALISE_LOGIN_LOCK_PATH=" + path}}
	s, err := PrepareArgv(e, "fixture", []string{exe, "-test.run=^TestLoginLockGuest$"})
	if err != nil {
		t.Fatal(err)
	}
	code, err := Run(home, s.Session, exe, 0, 0, lock)
	if err != nil || code != 7 {
		t.Fatal("login did not retain/inherit lock or preserve exit code", code, err)
	}
	if _, err := Run(home, s.Session, exe, 0, 0, nil); err == nil {
		t.Fatal("login ran without mutation lock")
	}
}

func TestPreparedLoginRechecksOwnershipBeforeExecution(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	p := profile.Profile{ID: "demo", ProfileVersion: 1}
	_, agent, err := porterenroll.AcceptRecord(home, enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": filepath.Join(home, "state")}}, p)
	if err != nil {
		t.Fatal(err)
	}
	e := &porterenroll.Env{Home: home, Vars: []string{"HOME=" + home}}
	s, err := write(e, "demo", "fixture", []string{"/nonexistent/old-program"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (environments.Store{Home: home}).Update(agent.Ref, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(agent.Ref) })
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.CreateTemp(home, "lock-")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	_, err = Run(home, s.Session, "/nonexistent/porter", 0, 0, lock)
	if err == nil || !strings.Contains(err.Error(), "environment_in_use") {
		t.Fatal("old login session ran after environment was claimed", err)
	}
}
