package service

import (
	"testing"
	"time"
)

// A backup time after now (the server's clock ran ahead) is never told as
// “just now”.
func TestAgoAtFuture(t *testing.T) {
	now := time.Date(2026, 11, 8, 21, 0, 0, 0, time.UTC)
	if got := agoAt(now.Add(-3*time.Hour), now); got != "3 小时前" {
		t.Fatalf("past: %q", got)
	}
	if got := agoAt(now.AddDate(0, 4, 0), now); got == "1 分钟前" || len(got) < 10 {
		t.Fatalf("future: %q", got)
	}
}
