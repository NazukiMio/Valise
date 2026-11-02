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
	if !l.ByID("rclone").Pending() {
		t.Fatal("rclone without sha256 must not be installable")
	}
	if got := For(l.ByID("rsync").Install, "ol"); len(got) == 0 || got[1] != "dnf" {
		t.Fatalf("%v", got)
	}
}

func TestLintRefuses(t *testing.T) {
	for name, y := range map[string]string{
		"enhanced runs commands": `{schema: valise-kit-tools/1, tools: [{id: a, tier: enhanced, name: a, purpose: p, without: w, check: {binary: a}, install: [{os_id: [debian], argv: [x]}]}]}`,
		"http download":          `{schema: valise-kit-tools/1, tools: [{id: a, tier: enhanced, name: a, purpose: p, without: w, check: {binary: a}, binary: {version: "1", archives: [{arch: amd64, url: "http://x/a.zip", sha256: "", member: a}]}}]}`,
		"other variable":         `{schema: valise-kit-tools/1, tools: [{id: a, tier: expert, name: a, purpose: p, without: w, check: {binary: a}, install: [{os_id: [debian], argv: [echo, "${PATH}"]}]}]}`,
		"unknown field":          `{schema: valise-kit-tools/1, tools: [{id: a, tier: expert, name: a, purpose: p, without: w, check: {binary: a}, script: "curl x | sh"}]}`,
		"member escapes":         `{schema: valise-kit-tools/1, tools: [{id: a, tier: enhanced, name: a, purpose: p, without: w, check: {binary: a}, binary: {version: "1", archives: [{arch: amd64, url: "https://x/a.zip", sha256: "", member: "../a"}]}}]}`,
	} {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "panic") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
