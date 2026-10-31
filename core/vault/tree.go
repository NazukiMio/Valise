package vault

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Vault tree, see docs/VAULT_FORMAT.md section 10: one valise-vault/1 vault
// (a leaf) per instance and period.
const (
	TreeFormat   = "valise-vault-tree/1"
	PeriodFormat = "valise-period/1"
	TreeFile     = "TREE"
	PeriodFile   = "PERIOD"
	DeletingFile = "DELETING"
	// LegacyLeaf names the vault from before the tree (one per server).
	LegacyLeaf = "-"
)

// Period units.
const (
	UnitWeek    = "week"
	UnitMonth   = "month"
	UnitQuarter = "quarter"
)

// TreeDesc is the TREE file.
type TreeDesc struct {
	Format     string    `json:"format"`
	ServerName string    `json:"server_name"`
	CreatedAt  time.Time `json:"created_at"`
}

// Period is the PERIOD file of a leaf. Start and End are dates in TZ,
// both inclusive.
type Period struct {
	Format     string `json:"format"`
	InstanceID string `json:"instance_id"`
	Unit       string `json:"unit"`
	TZ         string `json:"tz"`
	Start      string `json:"start"`
	End        string `json:"end"`
}

// Name is the leaf directory name.
func (p Period) Name() string { return p.Start + "_" + p.End }

// Leaf is the leaf name used by RPC: <instance>/<period>.
func (p Period) Leaf() string { return p.InstanceID + "/" + p.Name() }

// PeriodSetting is instances/<id>.vault.json.
type PeriodSetting struct {
	Unit string `json:"unit"`
	TZ   string `json:"tz"`
}

// DefaultPeriodSetting applies when an instance has no setting.
var DefaultPeriodSetting = PeriodSetting{Unit: UnitMonth, TZ: "UTC"}

var (
	periodNameRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_(\d{4}-\d{2}-\d{2})$`)
	instanceRe   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

const dateLayout = "2006-01-02"

// ValidPeriodName reports whether s is <start>_<end> with start <= end.
func ValidPeriodName(s string) bool {
	m := periodNameRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	a, err1 := time.Parse(dateLayout, m[1])
	b, err2 := time.Parse(dateLayout, m[2])
	return err1 == nil && err2 == nil && !b.Before(a)
}

// SplitLeaf validates a leaf name and splits it. The legacy leaf returns
// empty strings and ok.
func SplitLeaf(leaf string) (instance, period string, ok bool) {
	if leaf == LegacyLeaf {
		return "", "", true
	}
	instance, period, found := strings.Cut(leaf, "/")
	if !found || !instanceRe.MatchString(instance) || len(instance) > 64 || !ValidPeriodName(period) {
		return "", "", false
	}
	return instance, period, true
}

// Validate checks a setting; the time zone must be loadable.
func (s PeriodSetting) Validate() error {
	switch s.Unit {
	case UnitWeek, UnitMonth, UnitQuarter:
	default:
		return fmt.Errorf("周期只能是 week、month 或 quarter：%q", s.Unit)
	}
	if _, err := time.LoadLocation(s.TZ); err != nil || s.TZ == "" || s.TZ == "Local" {
		return fmt.Errorf("无效的时区 %q", s.TZ)
	}
	return nil
}

// periodBounds returns the first and last day of the unit containing day.
func periodBounds(unit string, day time.Time) (time.Time, time.Time) {
	y, m, d := day.Date()
	switch unit {
	case UnitWeek:
		off := (int(day.Weekday()) + 6) % 7 // Monday is 0
		start := time.Date(y, m, d-off, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 0, 6)
	case UnitQuarter:
		qm := time.Month((int(m)-1)/3*3 + 1)
		start := time.Date(y, qm, 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 3, -1)
	default:
		start := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 1, -1)
	}
}

// LocalDay is t's calendar date in the setting's time zone (as UTC midnight).
func (s PeriodSetting) LocalDay(t time.Time) time.Time {
	loc, err := time.LoadLocation(s.TZ)
	if err != nil {
		loc = time.UTC
	}
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Contains reports whether the day t falls in p (in p's own time zone).
func (p Period) Contains(t time.Time) bool {
	day := PeriodSetting{TZ: p.TZ}.LocalDay(t).Format(dateLayout)
	return day >= p.Start && day <= p.End
}

// NextPeriod is the leaf to create for a seal at t, following the last
// leaf (nil when the instance has none). See docs/VAULT_FORMAT.md 10.2.
func NextPeriod(instance string, last *Period, s PeriodSetting, t time.Time) Period {
	day := s.LocalDay(t)
	start, _ := periodBounds(s.Unit, day)
	if last != nil {
		if prevEnd, err := time.Parse(dateLayout, last.End); err == nil {
			if next := prevEnd.AddDate(0, 0, 1); next.After(start) {
				start = next
			}
		}
	}
	_, end := periodBounds(s.Unit, start)
	return Period{Format: PeriodFormat, InstanceID: instance, Unit: s.Unit, TZ: s.TZ,
		Start: start.Format(dateLayout), End: end.Format(dateLayout)}
}
