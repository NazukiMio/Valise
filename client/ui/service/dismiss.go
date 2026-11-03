package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"valise/client/config"
	"valise/core/valise"
)

// Dismissing a health item (docs/UI.md 9.5, 2026-11-02): a yellow one is
// dismissed for good (until the user lifts it); a red one only leaves the
// overview and home, stays red on the health page, and comes back after
// RedDismissDays. A dismissal no longer applies once the item gets worse.
// Valise only says how to fix an item; it never fixes it for the user.

const (
	dismissFile = "health-dismissed.json"
	// Red dismissals last 30 days by default, at most a year.
	RedDismissMin, RedDismissMax = 30, 365
)

// Dismissal is one dismissed health item; Forever for a yellow one.
type Dismissal struct {
	Level   string    `json:"level"` // the level when dismissed
	Until   time.Time `json:"until,omitempty"`
	Forever bool      `json:"forever,omitempty"`
}

// RedDismissDays is how long a red dismissal lasts.
func (s *Service) RedDismissDays() int {
	d := s.Config.RedDismissDays
	if d == 0 {
		return RedDismissMin
	}
	return max(RedDismissMin, min(RedDismissMax, d))
}

// SetRedDismissDays changes how long a red dismissal lasts (advanced
// settings); it applies to the next dismissal.
func (s *Service) SetRedDismissDays(days int) error {
	if days < RedDismissMin || days > RedDismissMax {
		return valise.New("red_dismiss_days_invalid", "红色提醒的忽略时长要在 30 到 365 天之间", "")
	}
	if err := config.SetRedDismissDays(s.Dirs.Data, days); err != nil {
		return err
	}
	s.Config.RedDismissDays = days
	return nil
}

type dismissals map[string]map[string]map[string]Dismissal // host → instance → item id

func (s *Service) loadDismissals() dismissals {
	out := dismissals{}
	if b, err := os.ReadFile(filepath.Join(s.Dirs.Data, dismissFile)); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func (s *Service) saveDismissals(d dismissals) error {
	now := time.Now()
	for h, insts := range d {
		for i, items := range insts {
			for id, x := range items {
				if !x.Forever && now.After(x.Until) {
					delete(items, id)
				}
			}
			if len(items) == 0 {
				delete(insts, i)
			}
		}
		if len(insts) == 0 {
			delete(d, h)
		}
	}
	b, _ := json.MarshalIndent(d, "", "  ")
	return os.WriteFile(filepath.Join(s.Dirs.Data, dismissFile), append(b, '\n'), 0o600)
}

// ToggleDismiss dismisses a warn item for good or a bad one for
// RedDismissDays, or lifts the dismissal; it reports whether the item is
// now dismissed.
func (s *Service) ToggleDismiss(host, instance string, it HealthItem) (bool, error) {
	if it.Level != HealthWarn && it.Level != HealthBad {
		return false, nil
	}
	d := s.loadDismissals()
	if d[host] == nil {
		d[host] = map[string]map[string]Dismissal{}
	}
	if d[host][instance] == nil {
		d[host][instance] = map[string]Dismissal{}
	}
	if _, ok := d[host][instance][it.ID]; ok && it.Dismissed != nil {
		delete(d[host][instance], it.ID)
		return false, s.saveDismissals(d)
	}
	x := Dismissal{Level: it.Level, Forever: it.Level == HealthWarn}
	if !x.Forever {
		x.Until = time.Now().Add(time.Duration(s.RedDismissDays()) * 24 * time.Hour)
	}
	d[host][instance][it.ID] = x
	return true, s.saveDismissals(d)
}

// MarkDismissed marks the dismissed items of instance on host (the
// caller adds checkup findings after Health).
func (s *Service) MarkDismissed(host, instance string, list []HealthItem) {
	items := s.loadDismissals()[host][instance]
	now := time.Now()
	for i := range list {
		list[i].Dismissed = nil
		x, ok := items[list[i].ID]
		if !ok || (!x.Forever && now.After(x.Until)) || Rank(list[i].Level) < Rank(x.Level) {
			continue // none, expired, or worse than when dismissed
		}
		until := x.Until // zero when forever
		list[i].Dismissed = &until
	}
}

// Quiet reports an item that should not be shown on the overview or home:
// any dismissed one.
func (it HealthItem) Quiet() bool { return it.Dismissed != nil }

// Counts reports whether the item counts as something to look at on the
// health page: warn or bad, and not a dismissed warn.
func (it HealthItem) Counts() bool {
	switch it.Level {
	case HealthBad:
		return true
	case HealthWarn:
		return it.Dismissed == nil
	}
	return false
}

// SetChatEntry turns the terminal UI's 对话 tab on or off.
func (s *Service) SetChatEntry(on bool) error {
	if err := config.SetChatEntryOff(s.Dirs.Data, !on); err != nil {
		return err
	}
	s.Config.ChatEntryOff = !on
	return nil
}
