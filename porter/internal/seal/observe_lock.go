package seal

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"valise/core/valise"
	"valise/porter/internal/actionjournal"
)

// ObservationLock takes the already existing recovery mutex without creating,
// truncating or rewriting its holder metadata. An observation is not a repair.
func (p Paths) ObservationLock() (func(), error) {
	f, err := os.OpenFile(filepath.Join(p.State(), "vault.lock"), os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || st.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, valise.New("environment_lock_unverified", "无法核实原操作的互斥文件，没有执行切换复核", "保留原记录，检查服务器状态目录后重新读取")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, valise.New("vault_busy", "服务器正在处理另一项操作，尚未读取切换条件", "等待原操作结束后重新读取")
		}
		return nil, err
	}
	if err := (actionjournal.Store{Home: p.Home}).Check(""); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
