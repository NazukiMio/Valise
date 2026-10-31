package vault

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNextPeriod(t *testing.T) {
	month := PeriodSetting{Unit: UnitMonth, TZ: "Asia/Shanghai"}
	p := NextPeriod("oc", nil, month, at("2026-10-31T17:00:00Z")) // Nov 1 01:00 in Shanghai
	if p.Name() != "2026-11-01_2026-11-30" || p.Leaf() != "oc/2026-11-01_2026-11-30" {
		t.Fatal(p.Name())
	}
	if !p.Contains(at("2026-11-30T15:59:00Z")) || p.Contains(at("2026-11-30T16:00:00Z")) {
		t.Fatal("Contains uses the period's time zone")
	}
	// Switching to weekly after a monthly leaf: the next leaf starts the
	// day after and ends with its week.
	week := PeriodSetting{Unit: UnitWeek, TZ: "Asia/Shanghai"}
	n := NextPeriod("oc", &p, week, at("2026-12-01T04:00:00Z"))
	if n.Name() != "2026-12-01_2026-12-06" { // Tue Dec 1 .. Sun Dec 6
		t.Fatal(n.Name())
	}
	// A long gap does not create a leaf that misses the seal date.
	g := NextPeriod("oc", &p, month, at("2027-03-10T04:00:00Z"))
	if g.Name() != "2027-03-01_2027-03-31" {
		t.Fatal(g.Name())
	}
	q := NextPeriod("oc", nil, PeriodSetting{Unit: UnitQuarter, TZ: "UTC"}, at("2026-08-15T00:00:00Z"))
	if q.Name() != "2026-07-01_2026-09-30" {
		t.Fatal(q.Name())
	}
}

func TestLeafNames(t *testing.T) {
	for leaf, ok := range map[string]bool{
		"oc/2026-10-01_2026-10-31": true, "-": true,
		"oc/2026-10-31_2026-10-01": false, "Oc/2026-10-01_2026-10-31": false,
		"oc/../x": false, "oc": false, "oc/2026-10-01": false, "oc/2026-13-01_2026-13-02": false,
	} {
		if _, _, got := SplitLeaf(leaf); got != ok {
			t.Errorf("%s: %v", leaf, got)
		}
	}
	if (PeriodSetting{Unit: "day", TZ: "UTC"}).Validate() == nil || (PeriodSetting{Unit: UnitWeek, TZ: "Mars/Base"}).Validate() == nil {
		t.Fatal("invalid setting accepted")
	}
}
