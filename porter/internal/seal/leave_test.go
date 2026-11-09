package seal

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Removing marks server.json; adding the server again clears the mark.
func TestMarkServerRemoved(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	if err := MarkServerRemoved(p, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.ServerFile()); !os.IsNotExist(err) {
		t.Fatal("an unnamed server got a server.json")
	}
	if err := SetServerName(p, "home-vps"); err != nil {
		t.Fatal(err)
	}
	MarkServerRemoved(p, time.Date(2026, 11, 9, 1, 2, 3, 4, time.UTC))
	b, _ := os.ReadFile(p.ServerFile())
	if !strings.Contains(string(b), `"removed_at": "2026-11-09T01:02:03Z"`) || !strings.Contains(string(b), `"name": "home-vps"`) {
		t.Fatalf("%s", b)
	}
	if err := SetServerName(p, "home-vps"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p.ServerFile())
	if strings.Contains(string(b), "removed_at") {
		t.Fatalf("mark kept: %s", b)
	}
}
