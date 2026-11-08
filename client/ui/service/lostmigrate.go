package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"valise/core/enroll"
	"valise/core/valise"
)

// A migration made while its source could not be reached
// (docs/ARCHITECTURE.md 8.2.3) is remembered on this computer: when the
// source answers again and still has the instance, both may be running.
// Valise only reminds; it stops neither side.

const lostMigrationsFile = "lost-migrations.json"

// LostMigration is one such migration.
type LostMigration struct {
	From       string    `json:"from"`
	Instance   string    `json:"instance"`
	Target     string    `json:"target"`
	OpID       string    `json:"op_id"`
	SnapshotID string    `json:"snapshot_id"`
	At         time.Time `json:"at"`
	// Settled is set when the person said it is dealt with, or the source
	// no longer has the instance.
	Settled *time.Time `json:"settled,omitempty"`
}

func (s *Service) loadLostMigrations() []LostMigration {
	var out []LostMigration
	if b, err := os.ReadFile(filepath.Join(s.Dirs.Data, lostMigrationsFile)); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func (s *Service) saveLostMigrations(list []LostMigration) error {
	b, _ := json.MarshalIndent(list, "", "  ")
	return os.WriteFile(filepath.Join(s.Dirs.Data, lostMigrationsFile), append(b, '\n'), 0o600)
}

func (s *Service) recordLostMigration(m LostMigration) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if unlock, err := s.lockLocation(ctx, "state", "lost-migrations", "all", true); err == nil {
		defer unlock()
	}
	_ = s.saveLostMigrations(append(s.loadLostMigrations(), m))
}

// LostMigrations lists this computer's unsettled lost-source migrations.
func (s *Service) LostMigrations() []LostMigration {
	var out []LostMigration
	for _, m := range s.loadLostMigrations() {
		if m.Settled == nil {
			out = append(out, m)
		}
	}
	return out
}

// LostSourceWarning is a reminder for a source that answers again.
type LostSourceWarning struct {
	LostMigration
	Text string `json:"text"`
}

// LostSourceWarnings checks host, now reachable, for instances migrated
// away while it was lost. An instance no longer there settles its record.
// Nothing is reported while host still cannot be read.
func (s *Service) LostSourceWarnings(ctx context.Context, host string) []LostSourceWarning {
	w, _ := s.CheckLostSource(ctx, host)
	return w
}

// CheckLostSource is LostSourceWarnings with the reason host could not be
// checked.
func (s *Service) CheckLostSource(ctx context.Context, host string) ([]LostSourceWarning, error) {
	var mine []LostMigration
	for _, m := range s.LostMigrations() {
		if m.From == host {
			mine = append(mine, m)
		}
	}
	if len(mine) == 0 {
		return nil, nil
	}
	insts, err := s.Instances(ctx, host)
	if err != nil {
		return nil, err
	}
	var out []LostSourceWarning
	var gone []string
	for _, m := range mine {
		if !slices.ContainsFunc(insts, func(i enroll.InstanceSummary) bool { return i.InstanceID == m.Instance }) {
			gone = append(gone, m.OpID)
			continue
		}
		out = append(out, LostSourceWarning{LostMigration: m, Text: host + " 又连上了，它上面仍有实例 " + m.Instance +
			"，而它已在连不上时迁移到 " + m.Target + "（" + m.At.Local().Format("01-02 15:04") + "）：两边可能都在运行，独占凭据不要两边同时使用。" +
			"处理好后（停用或移除其中一边）运行 valise revive lost --settle " + m.OpID})
	}
	if len(gone) > 0 {
		s.settleLost(gone)
	}
	return out, nil
}

// SettleLostMigration records that the person dealt with one.
func (s *Service) SettleLostMigration(op string) error {
	for _, m := range s.LostMigrations() {
		if m.OpID == op {
			s.settleLost([]string{op})
			return nil
		}
	}
	return valise.New("lost_unknown", "没有未处理的失联迁移 "+op, "用 valise revive lost 列出")
}

func (s *Service) settleLost(ops []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if unlock, err := s.lockLocation(ctx, "state", "lost-migrations", "all", true); err == nil {
		defer unlock()
	}
	list := s.loadLostMigrations()
	now := time.Now().UTC()
	for i := range list {
		if list[i].Settled == nil && slices.Contains(ops, list[i].OpID) {
			list[i].Settled = &now
		}
	}
	_ = s.saveLostMigrations(list)
}
