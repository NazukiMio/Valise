package recovery

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"

	core "valise/core/recovery"
	"valise/core/valise"
)

// A request placed with the user pass is answered with the host key: R
// comes back sealed to the one-time recipient, with a code both sides
// compute the same way.
func TestAnswer(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	kd := t.TempDir()
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(kd, "hk")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	priv, _ := os.ReadFile(filepath.Join(kd, "hk"))
	pub, _ := os.ReadFile(filepath.Join(kd, "hk.pub"))
	hostRcpt, err := agessh.ParseRecipient(string(bytes.TrimSpace(pub)))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := age.GenerateX25519Identity()
	var lock bytes.Buffer
	w, _ := age.Encrypt(&lock, hostRcpt)
	io.WriteString(w, r.String())
	w.Close()
	e, _ := age.GenerateX25519Identity()

	home := t.TempDir()
	id := "00112233445566778899aabbccddeeff"
	if _, err := Answer(priv, home); err == nil {
		t.Fatal("answered without a request")
	}
	if err := Place(home, core.RequestParams{ID: id, Lock: lock.Bytes(), Recipient: e.Recipient().String()}); err != nil {
		t.Fatal(err)
	}
	if _, err := Result(home, id); err == nil {
		t.Fatal("result before the command ran")
	} else if ue, _ := valise.As(err); ue == nil || ue.Code != core.WaitingCode {
		t.Fatal(err)
	}
	code, err := Answer(priv, home)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := Result(home, id)
	if err != nil || code != core.Code(id, ct) {
		t.Fatalf("code %s %v", code, err)
	}
	dec, err := age.Decrypt(bytes.NewReader(ct), e)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(dec)
	if string(got) != r.String() {
		t.Fatal("R did not come back")
	}
	if fi, _ := os.Stat(resPath(home, id)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	// A second answer does not overwrite the first.
	if _, err := Answer(priv, home); err == nil {
		t.Fatal("answered twice")
	}
	if err := Clear(home, id); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(dir(home)); len(left) != 0 {
		t.Fatal(left)
	}
	// A lock made for another host key is refused.
	other, _ := age.GenerateX25519Identity()
	var bad bytes.Buffer
	w, _ = age.Encrypt(&bad, other.Recipient())
	io.WriteString(w, "x")
	w.Close()
	Place(home, core.RequestParams{ID: id, Lock: bad.Bytes(), Recipient: e.Recipient().String()})
	if _, err := Answer(priv, home); err == nil {
		t.Fatal("wrong lock opened")
	}
	// The request file is never read through a symbolic link.
	Clear(home, id)
	os.Symlink(filepath.Join(kd, "hk"), reqPath(home, id))
	if _, err := Answer(priv, home); err == nil {
		t.Fatal("followed a link")
	}
}

func TestRunNeedsSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	if _, err := Run("me"); err == nil {
		t.Fatal("ran without root")
	}
}
