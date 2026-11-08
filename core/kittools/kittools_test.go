package kittools

import (
	"os"
	"strings"
	"testing"
)

func TestRepositoryList(t *testing.T) {
	b, err := os.ReadFile("../../kit/tools.yaml")
	if err != nil {
		t.Fatal(err)
	}
	l, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"rclone", "rsync", "linger"} {
		if l.ByID(id) == nil {
			t.Fatalf("no %s", id)
		}
	}
	if l.ByID("rclone").Pending() || !l.ByID("rsync").Pending() {
		t.Fatal("rclone is verified; rsync is not yet")
	}
	if got := For(l.ByID("rsync").Install, "ol"); len(got) == 0 || got[1] != "dnf" {
		t.Fatalf("%v", got)
	}
	rc := l.ByID("rclone")
	g := rc.Cloud("gdrive")
	if g == nil || !g.AskApp || g.Params["scope"] != "drive.file" || !g.UsesLoopback() || g.Pending() || g.Access != AccessFolder {
		t.Fatalf("%+v", g)
	}
	if od := rc.Cloud("onedrive"); od == nil || !od.Pending() {
		t.Fatalf("%+v", od)
	}
	if a := rc.ClientArchive("darwin", "arm64"); a == nil || !strings.Contains(a.URL, rc.Binary.Version) {
		t.Fatalf("%+v", a)
	}
}

func TestLintRefuses(t *testing.T) {
	for name, y := range map[string]string{
		"enhanced runs commands": `{schema: valise-kit-tools/1, tools: [{id: a, tier: enhanced, name: a, purpose: p, without: w, check: {binary: a}, install: [{os_id: [debian], argv: [x]}]}]}`,
		"http download":          `{schema: valise-kit-tools/1, tools: [{id: a, tier: enhanced, name: a, purpose: p, without: w, check: {binary: a}, binary: {version: "1", archives: [{arch: amd64, url: "http://x/a.zip", sha256: "", member: a}]}}]}`,
		"other variable":         `{schema: valise-kit-tools/1, tools: [{id: a, tier: expert, name: a, purpose: p, without: w, check: {binary: a}, install: [{os_id: [debian], argv: [echo, "${PATH}"]}]}]}`,
		"unknown field":          `{schema: valise-kit-tools/1, tools: [{id: a, tier: expert, name: a, purpose: p, without: w, check: {binary: a}, script: "curl x | sh"}]}`,
		"cloud token":            `{schema: valise-kit-tools/1, tools: [{id: rclone, tier: enhanced, name: a, purpose: p, without: w, check: {binary: rclone}, binary: {version: "1", archives: []}, clouds: [{id: x, name: X, rclone_type: drive, access: folder, access_note: n, params: {token: abc}}]}]}`,
		"cloud on other tool":    `{schema: valise-kit-tools/1, tools: [{id: a, tier: enhanced, name: a, purpose: p, without: w, check: {binary: a}, binary: {version: "1", archives: []}, clouds: [{id: x, name: X, rclone_type: drive, access: folder, access_note: n}]}]}`,
		"client http":            `{schema: valise-kit-tools/1, tools: [{id: rclone, tier: enhanced, name: a, purpose: p, without: w, check: {binary: rclone}, binary: {version: "1", archives: []}, client_archives: [{os: darwin, arch: arm64, url: "http://x/1/a.zip", sha256: "", member: a}]}]}`,
		"member escapes":         `{schema: valise-kit-tools/1, tools: [{id: a, tier: enhanced, name: a, purpose: p, without: w, check: {binary: a}, binary: {version: "1", archives: [{arch: amd64, url: "https://x/a.zip", sha256: "", member: "../a"}]}}]}`,
	} {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "panic") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
