package runtimeguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func isMissing(err error) bool { return errors.Is(err, os.ErrNotExist) }

func directory(home, rel string) error {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return unreadable()
	}
	current := home
	parts := []string{""}
	if rel != "" {
		parts = append(parts, splitPath(rel)...)
	}
	for _, part := range parts {
		if part != "" {
			current = filepath.Join(current, part)
		}
		st, err := os.Lstat(current)
		if err != nil {
			return err
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok || !st.IsDir() || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0022 != 0 {
			return unreadable()
		}
	}
	return nil
}
func splitPath(rel string) []string {
	var out []string
	for rel != "." && rel != "" {
		out = append([]string{filepath.Base(rel)}, out...)
		rel = filepath.Dir(rel)
	}
	return out
}

func read(home, rel string, out any, limit int64) error {
	if filepath.IsAbs(rel) || filepath.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return unreadable()
	}

	if err := directory(home, filepath.Dir(rel)); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(home, rel), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || st.Mode().Perm()&0077 != 0 || st.Size() > limit {
		return unreadable()
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(out); err != nil {
		return unreadable()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return unreadable()
	}
	return nil
}
