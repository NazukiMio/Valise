package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Dismissing a health item (docs/UI.md 9.5): a yellow one stops counting
// and leaves the overview and home; a red one only leaves the overview and
// home and stays red on the health page. Either expires on its own, and a
// dismissal no longer applies once the item gets worse.

const (
	dismissFile     = "health-dismissed.json"
	DismissDuration = 7 * 24 * time.Hour
)

// Dismissal is one dismissed health item.
type Dismissal struct {
	Level string    `json:"level"` // the level when dismissed
	Until time.Time `json:"until"`
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
				if now.After(x.Until) {
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

// ToggleDismiss dismisses a warn or bad item for DismissDuration, or
// lifts the dismissal; it reports whether the item is now dismissed.
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
	d[host][instance][it.ID] = Dismissal{Level: it.Level, Until: time.Now().Add(DismissDuration)}
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
		if !ok || now.After(x.Until) || Rank(list[i].Level) < Rank(x.Level) {
			continue // none, expired, or worse than when dismissed
		}
		until := x.Until
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
