package revive

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"valise/core/revive"
	"valise/core/valise"
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
			res.Entries = append(res.Entries, e)
		}
	}
	return res, nil
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
