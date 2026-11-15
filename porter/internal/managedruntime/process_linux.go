//go:build linux

package managedruntime

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func smallFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, ErrState
	}
	return b, nil
}

func emptyGroup(group, unit string) (bool, error) {
	p, err := groupPath(group, unit)
	if err != nil {
		return false, err
	}
	if p == "" {
		return true, nil
	}
	b, err := smallFile(filepath.Join(p, "cgroup.events"), 4096)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, ErrState
	}
	seen := false
	empty := false
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return false, ErrState
		}
		if fields[0] == "populated" {
			if seen || fields[1] != "0" && fields[1] != "1" {
				return false, ErrState
			}
			seen = true
			empty = fields[1] == "0"
		}
	}
	if !seen {
		return false, ErrState
	}
	return empty, nil
}

func processStart(pid int) (string, error) {
	base := filepath.Join("/proc", strconv.Itoa(pid))
	st, err := os.Stat(base)
	if err != nil {
		return "", ErrState
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) {
		return "", ErrState
	}
	b, err := smallFile(filepath.Join(base, "stat"), 8192)
	if err != nil {
		return "", ErrState
	}
	end := strings.LastIndex(string(b), ") ")
	if end < 0 {
		return "", ErrState
	}
	fields := strings.Fields(string(b)[end+2:])
	if len(fields) < 20 {
		return "", ErrState
	}
	if start, err := strconv.ParseUint(fields[19], 10, 64); err != nil || start == 0 {
		return "", ErrState
	}
	return fields[19], nil
}

func ownedMainProcess(pid int, group, unit string) bool {
	exe, _, err := nativeProcess(pid, group, unit)
	if err != nil {
		return false
	}
	expected, err := filepath.EvalSymlinks("/usr/bin/bwrap")
	return err == nil && exe == expected
}

func nativeProcess(pid int, group, unit string) (string, string, error) {
	if pid <= 0 || group == "" {
		return "", "", ErrState
	}
	base, err := groupPath(group, unit)
	if err != nil {
		return "", "", ErrState
	}
	start, err := processStart(pid)
	if err != nil {
		return "", "", ErrState
	}
	exe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return "", "", ErrState
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil || resolved != exe {
		return "", "", ErrState
	}
	file, err := os.Stat(exe)
	bound, bindErr := os.Stat(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil || bindErr != nil || !file.Mode().IsRegular() || file.Mode().Perm()&0111 == 0 || !os.SameFile(file, bound) {
		return "", "", ErrState
	}
	b, err := smallFile(filepath.Join(base, "cgroup.procs"), 128<<10)
	if err != nil {
		return "", "", ErrState
	}
	found := false
	for _, value := range strings.Fields(string(b)) {
		id, err := strconv.Atoi(value)
		if err != nil {
			return "", "", ErrState
		}
		if id == pid {
			found = true
		}
	}
	after, err := processStart(pid)
	if !found || err != nil || start != after {
		return "", "", ErrState
	}
	return exe, start, nil
}
