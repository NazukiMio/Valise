// Package revive implements staging, the journaled directory swap, health
// checks, rollback and crash recovery on the target server
// (docs/ARCHITECTURE.md section 8).
package revive

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"valise/core/revive"
	"valise/core/valise"
	"valise/porter/internal/activationrecord"
)

// Store keeps journals under ~/.local/state/valise/journal.
type Store struct{ Home string }

// Dir is the journal directory (unfinished operations).
func (s Store) Dir() string { return filepath.Join(s.Home, ".local", "state", "valise", "journal") }

// DoneDir holds finished operations.
func (s Store) DoneDir() string { return filepath.Join(s.Dir(), "done") }

func (s Store) path(op string) string { return filepath.Join(s.Dir(), op+".json") }

// Load reads an unfinished (or, failing that, finished) journal.
func (s Store) Load(op string) (*revive.Journal, error) {
	if !revive.ValidOpID(op) {
		return nil, valise.New("op_invalid", "操作 id 无效："+op, "使用 valise revive 显示的 op_ id")
	}
	b, err := os.ReadFile(s.path(op))
	if errors.Is(err, os.ErrNotExist) {
		b, err = os.ReadFile(filepath.Join(s.DoneDir(), op+".json"))
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, valise.New("op_unknown", "服务器上没有操作 "+op, "用 valise doctor 查看未完成的操作")
	}
	if err != nil {
		return nil, err
	}
	var j revive.Journal
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, valise.New("journal_corrupt", "交换日志无法解析："+op, "不要手动改动相关目录，联系项目处理").WithDetail(err.Error())
	}
	if j.ManagedData != nil {
		return activationrecord.LoadData(s.Home, op)
	}
	return &j, nil
}

// Save writes the journal atomically and fsyncs it.
func (s Store) Save(j *revive.Journal) error {
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir(), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	tmp.Chmod(0o600)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), s.path(j.Plan.OpID)); err != nil {
		return err
	}
	if d, err := os.Open(s.Dir()); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// Set records a state change. With fault "crash-at=<state>" the process
// exits right after the state is durable (drills only).
func (s Store) Set(j *revive.Journal, state string) error {
	j.State = state
	j.History = append(j.History, revive.Transition{State: state, At: time.Now().UTC()})
	if err := s.Save(j); err != nil {
		return err
	}
	if fault() == "crash-at="+state {
		fmt.Fprintf(os.Stderr, "valise-porter: injected crash at %s\n", state)
		os.Exit(3)
	}
	return nil
}

// Finish moves a final journal to done/.
func (s Store) Finish(j *revive.Journal) error {
	if err := os.MkdirAll(s.DoneDir(), 0o700); err != nil {
		return err
	}
	if err := os.Rename(s.path(j.Plan.OpID), filepath.Join(s.DoneDir(), j.Plan.OpID+".json")); err != nil {
		return err
	}
	if j.ManagedData != nil {
		return syncDataDirs(s.Dir(), s.DoneDir())
	}
	return nil
}

// Pending lists unfinished journals.
func (s Store) Pending() ([]*revive.Journal, error) {
	ents, err := os.ReadDir(s.Dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*revive.Journal
	for _, e := range ents {
		op, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !revive.ValidOpID(op) {
			continue
		}
		j, err := s.Load(op)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Plan.OpID < out[k].Plan.OpID })
	return out, nil
}

// Done lists finished journals.
func (s Store) Done() ([]*revive.Journal, error) {
	ents, err := os.ReadDir(s.DoneDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var out []*revive.Journal
	for _, e := range ents {
		op, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !revive.ValidOpID(op) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.DoneDir(), e.Name()))
		if err != nil {
			continue
		}
		var j revive.Journal
		if json.Unmarshal(b, &j) == nil {
			out = append(out, &j)
		}
	}
	return out, err
}

func fault() string { return os.Getenv(revive.FaultEnv) }

// sibling returns <parent of root>/.valise-<kind>-<op>-<id>.
func sibling(root, kind, op, id string) string {
	return filepath.Join(filepath.Dir(root), ".valise-"+kind+"-"+op+"-"+id)
}
