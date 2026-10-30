//go:build unix

package ssh

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// privateTmpDir returns /tmp/valise-<uid> if it is (or can be made) a real
// directory owned by the current user with mode 0700; otherwise "".
func privateTmpDir() string {
	uid := os.Getuid()
	dir := filepath.Join("/tmp", fmt.Sprintf("valise-%d", uid))
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return ""
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm() != 0o700 {
		return ""
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); !ok || int(sys.Uid) != uid {
		return ""
	}
	return dir
}
