package protection

import (
	"testing"
	"time"
)

func TestFullCycleKey(t *testing.T) {
	// 2026-11-29 is a Sunday, 2026-11-30 the next Monday.
	sun := time.Date(2026, 11, 29, 23, 0, 0, 0, time.UTC)
	mon := time.Date(2026, 11, 30, 0, 30, 0, 0, time.UTC)
	if FullCycleKey(FullCycleWeekly, sun, "UTC") == FullCycleKey(FullCycleWeekly, mon, "UTC") {
		t.Error("week boundary not detected")
	}
	if FullCycleKey(FullCycleMonthly, sun, "UTC") != FullCycleKey(FullCycleMonthly, mon.Add(-48*time.Hour), "UTC") {
		t.Error("same month differs")
	}
	if FullCycleKey(FullCycleMonthly, sun, "UTC") == FullCycleKey(FullCycleMonthly, mon.Add(24*time.Hour), "UTC") {
		t.Error("month boundary not detected")
	}
	// the timezone moves the boundary: Sunday 23:00 UTC is Monday in Shanghai
	if FullCycleKey(FullCycleWeekly, sun, "Asia/Shanghai") != FullCycleKey(FullCycleWeekly, mon, "Asia/Shanghai") {
		t.Error("timezone ignored")
	}
	if FullCycleKey("none", sun, "UTC") != "" || !FullTier("raw") || FullTier("soul") {
		t.Error("none / tier")
	}
}
