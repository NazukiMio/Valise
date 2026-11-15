package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"valise/core/revive"
	"valise/core/valise"
)

func restoreTestPending(t RestoreTest) bool {
	return t.State == "running" || t.State == "needs_cleanup"
}

func (s *Service) restoreTestLock(host, instance string) (func(), error) {
	if err := os.MkdirAll(s.Dirs.Data, 0700); err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(host + "\x00" + instance))
	unlock, err := protectionStoreLock(filepath.Join(s.Dirs.Data, fmt.Sprintf(".restore-test-%x.lock", key)))
	if err != nil {
		if e, ok := valise.As(err); ok && e.Code == "resource_busy" {
			return nil, valise.New("restore_test_busy", "另一个窗口正在处理这个 Agent 的备份测试", "回到原测试查看进度；完成后再试")
		}
		return nil, valise.New("restore_test_record", "无法打开本机测试记录", "检查文件权限后重试")
	}
	return unlock, nil
}

func (s *Service) readRestoreTests() (map[string]map[string]RestoreTest, error) {
	out := map[string]map[string]RestoreTest{}
	b, err := os.ReadFile(filepath.Join(s.Dirs.Data, restoreTestsFile))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil || json.Unmarshal(b, &out) != nil || out == nil {
		return nil, valise.New("restore_test_record", "无法读取本机的恢复测试记录", "检查记录文件的权限或备份；不要覆盖未核实的测试")
	}
	return out, nil
}

func (s *Service) loadRestoreTests() map[string]map[string]RestoreTest {
	all, _ := s.readRestoreTests()
	return all
}

func (s *Service) recordRestoreTest(host, instance string, t *RestoreTest) error {
	unlock, err := protectionStoreLock(filepath.Join(s.Dirs.Data, ".restore-tests.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	all, err := s.readRestoreTests()
	if err != nil {
		return err
	}
	if all[host] == nil {
		all[host] = map[string]RestoreTest{}
	}
	all[host][instance] = *t
	if err := protectionAtomic(filepath.Join(s.Dirs.Data, restoreTestsFile), all); err != nil {
		return valise.New("restore_test_record", "无法保存本机的测试记录", "检查磁盘空间和文件权限；测试已开始时请先核实原操作")
	}
	return nil
}

// LastRestoreTest is evidence about a particular tested backup, not a claim
// about the current process or the profile's runtime restore capability.
func (s *Service) LastRestoreTest(host, instance string) (RestoreTest, bool) {
	t, ok := s.loadRestoreTests()[host][instance]
	return t, ok
}

func (s *Service) localRestoreTestDir(op string) (string, error) {
	if !revive.ValidOpID(op) {
		return "", valise.New("restore_test_record", "原测试没有有效的操作记录", "保留现场并检查测试记录，不要手动指定清理路径")
	}
	return filepath.Join(s.Dirs.Cache, "restore-tests", op), nil
}

func (s *Service) cleanLocalRestoreTest(op string) error {
	dir, err := s.localRestoreTestDir(op)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(filepath.Dir(dir)); err != nil && !os.IsNotExist(err) || info != nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return valise.New("restore_test_local_cleanup", "无法核实本机测试暂存的位置", "检查缓存目录后再次核实并清理，不要改动 Agent 的目录")
	}
	if err := os.RemoveAll(dir); err != nil {
		return valise.New("restore_test_local_cleanup", "本机测试暂存尚未清理", "检查缓存目录权限后，再次核实并清理")
	}
	return nil
}
