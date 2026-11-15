package revive

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/revive"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

// leftovers are the directories an operation left next to live roots.
func leftovers(j *revive.Journal) []string {
	var out []string
	out = append(out, j.Intruders...)
	for _, jr := range j.Roots {
		for _, p := range []string{jr.Prev, jr.Failed, jr.Stage} {
			if exists(p) && strings.HasPrefix(filepath.Base(p), ".valise-") && p != jr.Live {
				out = append(out, p)
			}
		}
	}
	return out
}

// PrevList lists kept pre-swap states, failed states and stages of
// finished operations.
func PrevList(s Store) (*revive.PrevListResult, error) {
	done, err := s.Done()
	if err != nil {
		return nil, err
	}
	res := &revive.PrevListResult{Entries: []revive.PrevEntry{}}
	for _, j := range done {
		res.Entries = append(res.Entries, entries(j)...)
	}
	return res, nil
}

// entries describes the leftovers of one operation with their sizes.
func entries(j *revive.Journal) []revive.PrevEntry {
	var out []revive.PrevEntry
	for _, p := range leftovers(j) {
		e := revive.PrevEntry{OpID: j.Plan.OpID, Path: p}
		if len(j.History) > 0 {
			e.Created = j.History[len(j.History)-1].At
		}
		filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if info, err := d.Info(); err == nil {
					e.Bytes += info.Size()
				}
			}
			return nil
		})
		out = append(out, e)
	}
	return out
}

// OldList lists superseded instances and the directories the superseding
// operation kept.
func OldList(s Store) (*revive.OldListResult, error) {
	recs, err := porterenroll.ListRecords(s.Home)
	if err != nil {
		return nil, err
	}
	res := &revive.OldListResult{Entries: []revive.OldEntry{}}
	for _, r := range recs {
		if r.Superseded == nil {
			continue
		}
		e := revive.OldEntry{InstanceID: r.InstanceID, Superseded: *r.Superseded, Roots: r.Roots, Leftovers: []revive.PrevEntry{}}
		if j, err := s.Load(r.Superseded.OpID); err == nil && revive.Final(j.State) {
			e.Leftovers = append(e.Leftovers, entries(j)...)
		}
		res.Entries = append(res.Entries, e)
	}
	return res, nil
}

// OldCleanup removes a superseded instance: the directories its superseding
// operation kept, then its record and profile copy. It runs only on the
// user's explicit `valise old cleanup`; vault snapshots are not touched.
func OldCleanup(s Store, id string) ([]string, error) {
	rec, err := porterenroll.ReadRecord(s.Home, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+id, "用 valise old list 查看可清理的旧实例")
	}
	if rec.Superseded == nil {
		return nil, valise.New("instance_not_superseded", "实例 "+id+" 没有被取代，不能当作旧实例清理", "用 valise old list 查看可清理的旧实例")
	}
	var removed []string
	if j, err := s.Load(rec.Superseded.OpID); err == nil && revive.Final(j.State) {
		if removed, err = PrevDrop(s, j.Plan.OpID); err != nil {
			return removed, err
		}
	}
	if err := porterenroll.RemoveRecord(s.Home, id); err != nil {
		return removed, err
	}
	return append(removed, "实例档案中的 "+id), nil
}

// Finished returns when a finished operation ended (its leftovers were
// made then).
func Finished(s Store, op string) (time.Time, error) {
	j, err := s.Load(op)
	if err != nil {
		return time.Time{}, err
	}
	if !revive.Final(j.State) || len(j.History) == 0 {
		return time.Time{}, valise.New("op_unfinished", "操作 "+op+" 还没有结束", "先用 valise revive resume 处理")
	}
	return j.History[len(j.History)-1].At, nil
}

// PrevDrop deletes the leftovers of one finished operation. It runs only on
// the user's explicit `valise prev drop` and never touches a live root.
func PrevDrop(s Store, op string) ([]string, error) {
	j, err := s.Load(op)
	if err != nil {
		return nil, err
	}

	if !revive.Final(j.State) {
		return nil, valise.New("op_unfinished", "操作 "+op+" 还没有结束", "先用 valise revive resume 处理")
	}
	var removed []string
	for _, p := range leftovers(j) {
		if err := os.RemoveAll(p); err != nil {
			return removed, err
		}
		removed = append(removed, p)
	}
	return removed, nil
}
