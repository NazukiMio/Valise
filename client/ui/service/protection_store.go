package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"valise/core/protection"
)

func (c *ProtectionController) taskDir() string { return filepath.Join(c.config.StateDir, "tasks") }
func (c *ProtectionController) initialize() error {
	return c.withStore(func() error {
		p := filepath.Join(c.taskDir(), ".protection-executor.json")
		var record struct {
			ExecutorID string `json:"executor_id"`
		}
		b, err := os.ReadFile(p)
		if err == nil {
			if json.Unmarshal(b, &record) != nil || !protection.ValidID(record.ExecutorID) {
				return protectionError("record_corrupt", "客户端执行环境身份记录损坏")
			}
			if c.config.ExecutorID != "" && c.config.ExecutorID != record.ExecutorID {
				return protectionError("identity_conflict", "任务目录属于另一个客户端执行环境")
			}
			c.config.ExecutorID = record.ExecutorID
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return protectionError("record_corrupt", "无法读取客户端执行环境身份")
		}
		if c.config.ExecutorID == "" {
			c.config.ExecutorID, err = randomJourneyID()
			if err != nil {
				return err
			}
		}
		if !protection.ValidID(c.config.ExecutorID) {
			return protectionError("request_invalid", "客户端执行环境标识必须是随机 128 位标识")
		}
		record.ExecutorID = c.config.ExecutorID
		return protectionAtomic(p, record)
	})
}
func (c *ProtectionController) withStore(fn func() error) error {
	c.storeMu.Lock()
	defer c.storeMu.Unlock()
	dir := c.taskDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return protectionError("record_corrupt", "无法创建私有任务目录")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return protectionError("record_corrupt", "无法保护私有任务目录权限")
	}
	unlock, err := protectionStoreLock(filepath.Join(dir, ".protection.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}
func (c *ProtectionController) loadTasks() ([]ProtectionTask, error) {
	entries, err := os.ReadDir(c.taskDir())
	if err != nil {
		return nil, protectionError("record_corrupt", "无法读取任务索引")
	}
	var out []ProtectionTask
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "protection-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "protection-"), ".json")
		t, err := c.loadTask(id)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
func (c *ProtectionController) loadTask(id string) (ProtectionTask, error) {
	var task ProtectionTask
	if !protection.ValidID(id) {
		return task, protectionError("record_corrupt", "任务索引包含无效标识")
	}
	p := filepath.Join(c.taskDir(), "protection-"+id+".json")
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return task, protectionError("record_corrupt", "任务记录缺失或不可读")
	}
	b, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(b, &task) != nil || task.Schema != "valise-protection-task/1" || task.Kind != "protection_change" || task.TaskRef.TaskID != id || task.TaskRef.ExecutorID != c.config.ExecutorID || !protection.ValidID(task.ChangeID) || task.Revision == 0 || task.SubmissionKeyDigest == "" || task.RequestDigest == "" || task.RemoteRequestDigest == "" || task.Host == "" || protection.ValidateAgent(task.AgentRef) != nil {
		return ProtectionTask{}, protectionError("record_corrupt", "任务记录格式、身份或请求绑定无效")
	}
	switch task.State {
	case "waiting", "running", "reconciling", "needs_recovery", "succeeded", "failed", "cancelled":
	default:
		return ProtectionTask{}, protectionError("record_corrupt", "任务状态无法识别")
	}
	return task, nil
}
func (c *ProtectionController) saveTask(task ProtectionTask) error {
	return protectionAtomic(filepath.Join(c.taskDir(), "protection-"+task.TaskRef.TaskID+".json"), task)
}
func protectionAtomic(dst string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(dst)
	f, err := os.CreateTemp(dir, ".protection-write-")
	if err != nil {
		return protectionError("record_corrupt", "无法暂存私有任务记录")
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, dst)
	}
	if err == nil {
		var d *os.File
		d, err = os.Open(dir)
		if err == nil {
			err = d.Sync()
			_ = d.Close()
		}
	}
	if err != nil {
		return protectionError("record_corrupt", "无法持久保存私有任务记录，请先核实已发生的操作")
	}
	return nil
}
