package protection

import (
	"fmt"
	"time"
)

// FullCycleKey names the day/week/month bucket t falls in under cycle, in
// timezone tz; "" for no cycle. Two instants with the same key belong to the
// same cycle, so a lighter automatic run upgrades to full exactly when no
// automatic full snapshot carries the current key (protection-plan/4).
func FullCycleKey(cycle string, t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		loc = time.UTC
	}
	t = t.In(loc)
	switch cycle {
	case FullCycleWeekly:
		y, w := t.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", y, w)
	case FullCycleMonthly:
		return t.Format("2006-01")
	}
	return ""
}

// FullTier reports whether a snapshot tier already counts as a full snapshot
// of a cycle.
func FullTier(tier string) bool { return tier == "full" || tier == "raw" }
