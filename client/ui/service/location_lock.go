package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocationBusyError says another Valise process is copying to the same
// location (docs/ARCHITECTURE.md 7.4).
type LocationBusyError struct{ Location string }

func (e *LocationBusyError) Error() string { return "另一个 Valise 正在同步 " + e.Location }

// lockLocation takes this computer's lock for one server's folder in a
// copy location (each server copies to its own folder, so two servers may
// copy to one repository at once). Without
// wait a held lock returns *LocationBusyError at once (automatic copies
// skip); with wait it polls until the holder ends or ctx is done (copies
// the person started).
func (s *Service) lockLocation(ctx context.Context, kind, name, host string, wait bool) (func(), error) {
	dir := filepath.Join(s.Dirs.Data, "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "copy-"+kind+"-"+name+"-"+lockSafe(host)+".lock")
	for {
		unlock, held, err := tryFileLock(path)
		if err != nil || !held {
			return unlock, err
		}
		if !wait {
			return nil, &LocationBusyError{Location: name}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// lockSafe keeps a host alias usable in a file name.
func lockSafe(host string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_' {
			return r
		}
		return '_'
	}, host)
}
