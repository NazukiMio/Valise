package login

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	porterenroll "valise/porter/internal/enroll"
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
