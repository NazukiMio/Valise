package gui

import (
	"encoding/json"
	"strings"
	"testing"

	"valise/core/identity"
	"valise/core/protection"
)

// After a re-enrollment the earlier plan is the starting point. When all
// its copies went to another server's targets, none is carried over, and
// the plan still says so with an empty list: "按原设置重新生效" and a
// changed plan both pass the server's check (a missing list is refused
// as request_invalid).
func TestSupersededPlanWithoutCopiesStaysValid(t *testing.T) {
	on := true
	ref := identity.AgentRef{ServerUID: "u", InstallID: "openclaw", InstanceID: "xiaoman", Generation: "new"}
	old := ref
	old.Generation = "old"
	plan := func(copies []protection.ServerCopy) *protState {
		return &protState{ref: &ref, view: &protection.View{Mode: "superseded", SupersededPlan: &protection.ServerPlan{AgentRef: old, Revision: 3,
			Desired: protection.Desired{Scope: protection.Scope{Kind: "preset", Tier: "full"},
				Schedule: protection.Schedule{Enabled: &on, DailyAt: "04:40", Timezone: "Asia/Shanghai"},
				Period:   protection.Period{Unit: "month", Timezone: "Asia/Shanghai"}, ServerCopies: copies}}}}
	}
	elsewhere := protection.ServerCopy{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "nas", AuthorityID: "other"}, Enabled: &on, MinimumInterval: "1h"}
	here := protection.ServerCopy{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "gdrive", AuthorityID: "u"}, Enabled: &on, MinimumInterval: "1h"}
	for name, copies := range map[string][]protection.ServerCopy{"only another server's": {elsewhere}, "none": {}, "missing": nil} {
		d := plan(copies).desired()
		if d.ServerCopies == nil || len(d.ServerCopies) != 0 {
			t.Fatalf("%s: copies %#v", name, d.ServerCopies)
		}
		// As it is: "按原设置重新生效".
		if err := d.Validate(); err != nil {
			t.Fatalf("%s: the earlier plan as it is: %v", name, err)
		}
		// Changed: another time of day.
		d.Schedule.DailyAt = "05:10"
		if err := d.Validate(); err != nil {
			t.Fatalf("%s: a changed plan: %v", name, err)
		}
		b, _ := json.Marshal(protection.PrepareParams{AgentRef: ref, ExpectedRevision: 3, Desired: d})
		if !strings.Contains(string(b), `"server_copies":[]`) {
			t.Fatalf("%s: the request does not say an empty list: %s", name, b)
		}
	}
	// This server's own copies are kept.
	if d := plan([]protection.ServerCopy{here, elsewhere}).desired(); len(d.ServerCopies) != 1 || d.ServerCopies[0].TargetRef.ID != "gdrive" || d.Validate() != nil {
		t.Fatalf("kept %#v, %v", d.ServerCopies, d.Validate())
	}
	// Whatever a page hands over, the request says its copies.
	var bare protection.Desired
	if got := sayCopies(bare); got.ServerCopies == nil {
		t.Fatal("a plan without a list was sent on as it is")
	}
	// The plan in force is read the same way.
	inForce := &protState{ref: &ref, view: &protection.View{ServerPlan: &protection.ServerPlan{Desired: protection.Desired{}}}}
	if inForce.desired().ServerCopies == nil {
		t.Fatal("the plan in force lost its list")
	}
}
