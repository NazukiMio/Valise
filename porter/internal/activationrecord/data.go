package activationrecord

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"valise/core/revive"
)

// LoadData reads an existing managed child, including an archived child. It
// deliberately does not use the legacy reader's migration/lenient path.
func LoadData(home, op string) (*revive.Journal, error) {
	if !revive.ValidOpID(op) || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return nil, recordError()
	}
	base := home
	for _, part := range []string{"", ".local", "state", "valise", "journal"} {
		if part != "" {
			base = filepath.Join(base, part)
		}
		if err := ownedDirectory(base); err != nil {
			return nil, err
		}
	}
	j, err := readDataFile(filepath.Join(base, op+".json"), op)
	if !errors.Is(err, os.ErrNotExist) {
		return j, err
	}
	if err := ownedDirectory(filepath.Join(base, "done")); err != nil {
		return nil, err
	}
	return readDataFile(filepath.Join(base, "done", op+".json"), op)
}

func ownedDirectory(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0022 != 0 {
		return recordError()
	}
	return nil
}

func readDataFile(path, op string) (*revive.Journal, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || st.Mode().Perm()&0077 != 0 || st.Size() > 96<<20 {
		return nil, recordError()
	}
	b, err := io.ReadAll(io.LimitReader(f, 96<<20+1))
	if err != nil {
		return nil, err
	}
	var j revive.Journal
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&j) != nil || j.Plan.OpID != op || j.ManagedData == nil {
		return nil, recordError()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, recordError()
	}
	return &j, nil
}
