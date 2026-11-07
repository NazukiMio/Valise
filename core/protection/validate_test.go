package protection

import (
	"encoding/json"
	"strings"
	"testing"

	"valise/core/identity"
)

func validDesired() Desired {
	d := NewDefault()
	d.Schedule.DailyAt = "02:30"
	d.Schedule.Timezone = "America/New_York"
	d.Period = Period{Unit: "month", Timezone: "Asia/Shanghai"}
	return d
}
func TestExplicitCompleteDesired(t *testing.T) {
	d := NewDefault()
	if d.Scope.Tier != "full" || *d.Schedule.Enabled {
		t.Fatal("new default must be full and disabled")
	}
	if d.Validate() == nil {
		t.Fatal("missing schedule/period was accepted")
	}
	d = validDesired()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Desired){
		"missing enabled": func(d *Desired) { d.Schedule.Enabled = nil },
		"missing copies":  func(d *Desired) { d.ServerCopies = nil },
		"custom":          func(d *Desired) { d.Scope.Kind = "custom" },
		"bad daily":       func(d *Desired) { d.Schedule.DailyAt = "25:01" },
		"fixed offset":    func(d *Desired) { d.Schedule.Timezone = "+08:00" },
		"local":           func(d *Desired) { d.Period.Timezone = "Local" },
		"specifiers":      func(d *Desired) { d.Schedule.Timezone = "UTC%h" },
	} {
		t.Run(name, func(t *testing.T) {
			d := validDesired()
			mutate(&d)
			if d.Validate() == nil {
				t.Fatal("accepted invalid full replacement")
			}
		})
	}
	if d.Schedule.Timezone == d.Period.Timezone {
		t.Fatal("test must cover independent timezones")
	}
}
func TestExactRevisionJSON(t *testing.T) {
	var r Revision
	if err := json.Unmarshal([]byte(`"18446744073709551615"`), &r); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if string(b) != `"18446744073709551615"` {
		t.Fatal(string(b))
	}
	for _, s := range []string{`1`, `null`, `"01"`, `"-1"`, `"18446744073709551616"`} {
		if json.Unmarshal([]byte(s), &r) == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
func TestFrozenDigestBindsTargetVersionAndCopies(t *testing.T) {
	f := FrozenChange{Kind: "protection_change", Request: PrepareParams{AgentRef: identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "generation"}, Desired: validDesired()}}
	a, _ := f.Digest()
	f.Request.AgentRef.Generation = "other"
	b, _ := f.Digest()
	if a == b {
		t.Fatal("generation not bound")
	}
	f.Request.ExpectedRevision++
	c, _ := f.Digest()
	if b == c {
		t.Fatal("revision not bound")
	}
	f.Request.Desired.Scope.Tier = "soul"
	d, _ := f.Digest()
	if c == d {
		t.Fatal("scope not bound")
	}
	if strings.Contains(f.Request.Desired.Schedule.Calendar(), "%") {
		t.Fatal("calendar permits specifiers")
	}
}
