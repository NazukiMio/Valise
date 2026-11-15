package environments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"valise/core/profile"

	"valise/core/vault"
)

func observeRuntimeSource(ctx context.Context, in InstalledSource) (*runtimeSource, error) {
	unit := in.Profile.Environments.RuntimeExecutable
	pid, err := runtimeMainPID(ctx, in.Layout.Home, unit.Service.Unit)
	if err != nil {
		return nil, err
	}
	start, err := runtimeProcessStart(pid)
	if err != nil {
		return nil, err
	}
	proc := "/proc/" + strconv.Itoa(pid)
	owner, err := os.Stat(proc)
	if err != nil || !sourceOwned(owner) {
		return nil, ErrSource
	}
	exe, err := os.Readlink(proc + "/exe")
	if err != nil || filepath.Base(exe) != unit.Name || filepath.Clean(exe) != exe || !strings.HasPrefix(exe, in.Layout.Home+"/") {
		return nil, ErrSource
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil || real != exe {
		return nil, ErrSource
	}
	if sourceCategory(in, filepath.ToSlash(strings.TrimPrefix(exe, in.Layout.Home+"/"))) != profile.CategoryBody {
		return nil, ErrSource
	}
	f, err := os.OpenFile(exe, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrSource
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !sourceOwned(st) || st.Mode().Perm()&0o100 == 0 || st.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 || st.Size() < 0 || st.Size() > in.MaxBytes {
		return nil, ErrSource
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(&contextReader{ctx, f}, st.Size()+1))
	after, statErr := f.Stat()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || statErr != nil || n != st.Size() || after.Size() != st.Size() || after.Mode() != st.Mode() || !after.ModTime().Equal(st.ModTime()) {
		return nil, ErrSource
	}
	end, err := runtimeProcessStart(pid)
	bound, linkErr := os.Stat(proc + "/exe")
	if err != nil || end != start || linkErr != nil || !os.SameFile(st, bound) {
		return nil, ErrSource
	}
	checksum := hex.EncodeToString(h.Sum(nil))
	return &runtimeSource{PID: pid, Start: start,
		File:       ProgramFile{Path: filepath.ToSlash(strings.TrimPrefix(exe, in.Layout.Home+"/")), Size: st.Size(), SHA256: checksum, Mode: uint32(st.Mode().Perm() & 0o700)},
		Dependency: vault.RuntimeDependency{Role: "runtime-executable", Path: exe, Resolved: real, Size: st.Size(), SHA256: checksum}}, nil
}

func runtimeProcessStart(pid int) (string, error) {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", ErrSource
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	i := bytes.LastIndexByte(b, ')')
	if err != nil || len(b) > 8192 || i < 0 {
		return "", ErrSource
	}
	fields := strings.Fields(string(b[i+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return "", ErrSource
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", ErrSource
	}
	return fields[19], nil
}

func runtimeMainPID(ctx context.Context, home, unit string) (int, error) {
	st, err := os.Stat("/usr/bin/systemctl")
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o022 != 0 {
		return 0, ErrSource
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "--user", "show", "--property=MainPID", "--property=ActiveState", "--", unit)
	runDir := "/run/user/" + strconv.Itoa(os.Getuid())
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C", "XDG_RUNTIME_DIR=" + runDir, "DBUS_SESSION_BUS_ADDRESS=unix:path=" + runDir + "/bus"}
	cmd.Dir = "/"
	var out runtimeMetadata
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return 0, ErrSource
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		key, value, ok := strings.Cut(line, "=")
		_, duplicate := fields[key]
		if !ok || (key != "MainPID" && key != "ActiveState") || duplicate {
			return 0, ErrSource
		}
		fields[key] = value
	}
	pid, err := strconv.Atoi(fields["MainPID"])
	if err != nil || pid <= 1 || fields["ActiveState"] != "active" {
		return 0, ErrSource
	}
	return pid, nil
}

type runtimeMetadata struct{ bytes.Buffer }

func (m *runtimeMetadata) Write(b []byte) (int, error) {
	if m.Len()+len(b) > 1024 {
		return 0, ErrSource
	}
	return m.Buffer.Write(b)
}
