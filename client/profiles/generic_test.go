package profiles

import (
	"strings"
	"testing"
)

func TestBuildGenericProfileValidAndSanitized(t *testing.T) {
	id, raw, eff, err := BuildGenericProfile(GenericProfileInput{
		DisplayName: "My Cool Agent",
		StateDir:    "~/.my-agent/",
		SystemdUnit: "my-agent-gateway",
		NativeCLI:   "myagent",
	})
	if err != nil {
		t.Fatalf("BuildGenericProfile failed: %v", err)
	}
	if id != "custom-my-cool-agent" || eff.ID != id || eff.DisplayName != "My Cool Agent" || eff.Status != "draft" {
		t.Fatalf("unexpected profile meta: id=%q eff=%+v", id, eff)
	}
	if eff.Detect == nil || len(eff.Detect.StateDirCandidates) != 1 || eff.Detect.StateDirCandidates[0] != "${HOME}/.my-agent" {
		t.Fatalf("unexpected detect: %+v", eff.Detect)
	}
	if eff.Service == nil || len(eff.Service.Candidates) != 1 || eff.Service.Candidates[0].Unit != "my-agent-gateway.service" {
		t.Fatalf("unexpected service: %+v", eff.Service)
	}
	if len(eff.NativeCLI) != 1 || eff.NativeCLI[0] != "myagent" {
		t.Fatalf("unexpected native_cli: %v", eff.NativeCLI)
	}
	if len(eff.Paths) != 0 {
		t.Fatalf("generic profile must declare zero guessed paths, got %+v", eff.Paths)
	}
	if !strings.Contains(string(raw), "schema: valise-profile/1") {
		t.Fatalf("unexpected YAML:\n%s", string(raw))
	}

	// Non-ASCII DisplayName hashes deterministically into custom-<8hex> and optional fields stay clean.
	idZH, _, effZH, err := BuildGenericProfile(GenericProfileInput{
		DisplayName: "我的自定义助手",
		StateDir:    "${HOME}/.config/zh-agent",
	})
	if err != nil {
		t.Fatalf("BuildGenericProfile non-ASCII failed: %v", err)
	}
	if !strings.HasPrefix(idZH, "custom-") || len(idZH) != len("custom-")+8 || effZH.Service != nil || len(effZH.NativeCLI) != 0 {
		t.Fatalf("unexpected non-ASCII profile: id=%q eff=%+v", idZH, effZH)
	}
}

func TestBuildGenericProfileRejections(t *testing.T) {
	for name, in := range map[string]GenericProfileInput{
		"empty name":        {DisplayName: "", StateDir: "~/.a"},
		"ctrl in name":      {DisplayName: "bad\nname", StateDir: "~/.a"},
		"home root":         {DisplayName: "Agent", StateDir: "~"},
		"outside home":      {DisplayName: "Agent", StateDir: "/etc/agent"},
		"dotdot traversal":  {DisplayName: "Agent", StateDir: "~/../../etc"},
		"extra var":         {DisplayName: "Agent", StateDir: "${HOME}/${FOO}"},
		"shell metachar":    {DisplayName: "Agent", StateDir: "~/.a;rm -rf"},
		"bad unit":          {DisplayName: "Agent", StateDir: "~/.a", SystemdUnit: "bad/unit.service"},
		"cli with args":     {DisplayName: "Agent", StateDir: "~/.a", NativeCLI: "myagent --port 80"},
		"cli with path":     {DisplayName: "Agent", StateDir: "~/.a", NativeCLI: "/usr/bin/myagent"},
		"invalid custom id": {ID: "bad_id!", DisplayName: "Agent", StateDir: "~/.a"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := BuildGenericProfile(in); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}
