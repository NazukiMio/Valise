package recovery

import (
	"encoding/json"
	"io"
	"os"
	"syscall"
)

func encode(v any) []byte { b, _ := json.Marshal(v); return b }

func decode(b []byte, v any) error { return json.Unmarshal(b, v) }

// readNoFollow reads a small file without following a symbolic link.
func readNoFollow(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxFile))
}

// writeNew creates a private file that must not exist yet.
func writeNew(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
