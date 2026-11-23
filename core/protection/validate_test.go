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

func TestDisabledScheduleAllowsEmptyDailyAtAndV3Cadences(t *testing.T) {
	// Bug A fix: a disabled schedule with empty DailyAt (and valid Period) validates cleanly.
	d := NewDefault()
	d.Period = Period{Unit: "month", Timezone: "Asia/Shanghai"}
	if err := d.Validate(); err != nil {
		t.Fatalf("disabled schedule with empty DailyAt rejected: %v", err)
	}
	if d.Schedule.EffectiveCadence() != "daily" || d.Schedule.UsesV3() {
		t.Fatalf("default cadence/v3 mismatch: %+v", d.Schedule)
	}

	yes := true
	// Enabled without DailyAt for daily cadence must still fail.
	d.Schedule.Enabled = &yes
	d.Schedule.Timezone = "UTC"
	if err := d.Validate(); err == nil {
		t.Fatal("enabled daily schedule without DailyAt accepted")
	}

	// Hourly cadence
	d.Schedule = Schedule{Enabled: &yes, Cadence: "hourly", EveryHours: 6, DailyAt: "00:15", Timezone: "UTC"}
	if err := d.Validate(); err != nil {
		t.Fatalf("hourly schedule rejected: %v", err)
	}
	if got := d.Schedule.Calendar(); got != "*-*-* 00/6:15:00 UTC" || !d.Schedule.UsesV3() {
		t.Fatalf("hourly calendar=%q usesV3=%v", got, d.Schedule.UsesV3())
	}
	// Hourly with empty DailyAt defaults minute to 00.
	d.Schedule.DailyAt = ""
	if err := d.Validate(); err != nil || d.Schedule.Calendar() != "*-*-* 00/6:00:00 UTC" {
		t.Fatalf("hourly empty DailyAt: err=%v cal=%q", err, d.Schedule.Calendar())
	}
	// Invalid hourly interval (5h) rejected.
	d.Schedule.EveryHours = 5
	if err := d.Validate(); err == nil {
		t.Fatal("invalid EveryHours=5 accepted")
	}

	// Weekly cadence
	d.Schedule = Schedule{Enabled: &yes, Cadence: "weekly", Weekdays: []string{"Fri", "Mon"}, DailyAt: "03:00", Timezone: "Asia/Shanghai"}
	if err := d.Validate(); err != nil {
		t.Fatalf("weekly schedule rejected: %v", err)
	}
	if got := d.Schedule.Calendar(); got != "Mon,Fri *-*-* 03:00:00 Asia/Shanghai" || !d.Schedule.UsesV3() {
		t.Fatalf("weekly calendar=%q usesV3=%v", got, d.Schedule.UsesV3())
	}
	// Duplicate or unknown weekday rejected.
	d.Schedule.Weekdays = []string{"Mon", "Mon"}
	if err := d.Validate(); err == nil {
		t.Fatal("duplicate weekday accepted")
	}
	d.Schedule.Weekdays = []string{"Funday"}
	if err := d.Validate(); err == nil {
		t.Fatal("unknown weekday accepted")
	}

	// Monthly cadence
	d.Schedule = Schedule{Enabled: &yes, Cadence: "monthly", DayOfMonth: 5, DailyAt: "04:30", Timezone: "UTC"}
	if err := d.Validate(); err != nil {
		t.Fatalf("monthly schedule rejected: %v", err)
	}
	if got := d.Schedule.Calendar(); got != "*-*-05 04:30:00 UTC" || !d.Schedule.UsesV3() {
		t.Fatalf("monthly calendar=%q usesV3=%v", got, d.Schedule.UsesV3())
	}
	for _, badDay := range []int{0, 29, 31, -1} {
		d.Schedule.DayOfMonth = badDay
		if err := d.Validate(); err == nil {
			t.Fatalf("invalid DayOfMonth=%d accepted", badDay)
		}
	}
}
