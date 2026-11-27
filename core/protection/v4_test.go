package protection

import (
	"encoding/json"
	"testing"

	"valise/core/identity"
)

func TestFullCycleAndBundleMembersValidation(t *testing.T) {
	d := validDesired()
	for _, ok := range []string{"", "none", "weekly", "monthly"} {
		d.Schedule.FullCycle = ok
		if err := d.Validate(); err != nil {
			t.Errorf("full_cycle %q: %v", ok, err)
		}
	}
	d.Schedule.FullCycle = "daily"
	if d.Validate() == nil {
		t.Error("bad full_cycle accepted")
	}
	d.Schedule.FullCycle = "weekly"
	if !d.UsesV4() {
		t.Error("full cycle must need v4")
	}
	if validDesired().UsesV4() {
		t.Error("plain plan must not need v4")
	}
	for name, members := range map[string][]string{"single": {"a"}, "no self": {"b", "c"}, "dup": {"a", "a"}, "bad id": {"a", "B!"}} {
		if err := ValidateBundleMembers("a", members); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := ValidateBundleMembers("a", []string{"a", "b"}); err != nil {
		t.Error(err)
	}
	if err := ValidateBundleMembers("a", nil); err != nil {
		t.Error(err)
	}
	ref := identity.AgentRef{ServerUID: "u", InstallID: "a", Generation: "g", InstanceID: "a"}
	if err := (PrepareParams{AgentRef: ref, Desired: func() Desired { x := validDesired(); x.BundleMembers = []string{"b", "c"}; return x }()}).Validate(); err == nil {
		t.Error("bundle without self accepted")
	}
}

func TestSelectionTargets(t *testing.T) {
	if err := (BackupSelection{Targets: []string{"nas", "cloud"}}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []BackupSelection{{Targets: []string{"a", "a"}}, {Targets: []string{"a b"}}, {Tier: "full", SkipCopies: true, Targets: []string{"a"}}} {
		if s.Validate() == nil {
			t.Errorf("accepted %+v", s)
		}
	}
	p := BackupParams{AgentRef: identity.AgentRef{ServerUID: "u", InstallID: "a", Generation: "g", InstanceID: "a"}, ExpectedRevision: 1,
		RunID: "0123456789abcdef0123456789abcdef", Reason: "timer", Selection: BackupSelection{Targets: []string{"nas"}}}
	if _, err := p.Normalize(); err == nil {
		t.Error("timer run with targets accepted")
	}
	p.Reason = "manual"
	if _, err := p.Normalize(); err != nil {
		t.Errorf("manual targets: %v", err)
	}
}

func TestPrepareParamsStrictBundleFields(t *testing.T) {
	var p PrepareParams
	raw := `{"agent_ref":{},"expected_revision":"0","desired":{"bogus":1}}`
	if json.Unmarshal([]byte(raw), &p) == nil {
		t.Fatal("unknown field accepted")
	}
	raw = `{"agent_ref":{},"expected_revision":"0","desired":{"bundle_members":["a","b"],"schedule":{"full_cycle":"weekly"}}}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil || len(p.Desired.BundleMembers) != 2 || p.Desired.Schedule.FullCycle != "weekly" {
		t.Fatalf("%v %+v", err, p.Desired)
	}
}
