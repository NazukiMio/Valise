// Package actionjournal persists configuration operations independently of revive.
package actionjournal

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/action"
	"valise/core/valise"
)

type Store struct{ Home string }

func (s Store) Dir() string {
	return filepath.Join(s.Home, ".local", "state", "valise", "action-journal")
}
func (s Store) path(id string, done bool) string {
	d := s.Dir()
	if done {
		d = filepath.Join(d, "done")
	}
	return filepath.Join(d, id+".json")
}
func invalid() error {
	return valise.New("action_journal_corrupt", "配置操作记录无法核实", "保留相关目录，不要手动删除或覆盖；检查操作记录")
}
func syncDir(p string) error {
	d, err := os.Open(p)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s Store) Load(id string) (*action.Operation, error) {
	if !action.ValidOperationID(id) {
		return nil, invalid()
	}
	p := s.path(id, false)
	st, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		p = s.path(id, true)
		st, err = os.Lstat(p)
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return nil, invalid()
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	var j action.Operation
	if dec.Decode(&j) != nil || j.Format != action.OperationFormat || j.ID != id || dec.Decode(new(any)) != io.EOF {
		return nil, invalid()
	}
	return &j, nil
}
func (s Store) Save(j *action.Operation) error {
	if !action.ValidOperationID(j.ID) || j.Format != action.OperationFormat {
		return invalid()
	}
	dir := s.Dir()
	if j.Done {
		dir = filepath.Join(dir, "done")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return invalid()
	}
	j.Updated = time.Now().UTC()
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".writing-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path(j.ID, j.Done)); err != nil {
		return err
	}
	if err = syncDir(dir); err != nil {
		return err
	}
	if j.Done {
		if err = os.Remove(s.path(j.ID, false)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return syncDir(s.Dir())
	}
	return nil
}
func (s Store) Pending() ([]*action.Operation, error) {
	es, err := os.ReadDir(s.Dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*action.Operation
	for _, e := range es {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !action.ValidOperationID(id) {
			return nil, invalid()
		}
		j, err := s.Load(id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

// Check blocks conflicting writes until the user explicitly resolves the record.
// The execution lock must also be held by the caller for mutation.
func (s Store) Check(except string) error {
	js, err := s.Pending()
	if err != nil {
		return err
	}
	for _, j := range js {
		if j.ID != except {
			return valise.New("action_pending", "服务器有未完成的配置操作 "+j.ID, "查看操作状态并确认恢复原服务状态，或继续发布后再重试")
		}
	}
	return nil
}
