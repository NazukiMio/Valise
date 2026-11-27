// Package seal implements sealing, vault status, verification and sync on
// the server (docs/ARCHITECTURE.md sections 6 and 7).
package seal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"valise/core/valise"
	"valise/porter/internal/actionjournal"
	porterenroll "valise/porter/internal/enroll"
)

// Paths are the porter's files under HOME (docs/ARCHITECTURE.md section 1).
type Paths struct{ Home string }

func (p Paths) Recipients() string {
	return filepath.Join(p.Home, ".config", "valise", "recipients.txt")
}
func (p Paths) Targets() string { return filepath.Join(p.Home, ".config", "valise", "targets.json") }
func (p Paths) State() string   { return filepath.Join(p.Home, ".local", "state", "valise") }
func (p Paths) Tmp() string     { return filepath.Join(p.State(), "tmp") }

// RcloneConfig is Valise's own rclone config file (docs/ARCHITECTURE.md 7.3).
func (p Paths) RcloneConfig() string {
	return filepath.Join(p.Home, ".config", "valise", "rclone", "rclone.conf")
}

// Rclone is the Kit-installed rclone.
func (p Paths) Rclone() string { return filepath.Join(p.Home, ".local", "bin", "rclone") }
func (p Paths) Audit() string  { return filepath.Join(p.State(), "audit.jsonl") }
func (p Paths) SyncState(target string) string {
	return filepath.Join(p.State(), "sync", target+".json")
}

// DedupeKey is ~/.config/valise/dedupe/<vault_id>.key.
func (p Paths) DedupeKey(vaultID string) string {
	return filepath.Join(p.Home, ".config", "valise", "dedupe", vaultID+".key")
}

// Lock takes the vault lock (flock) so only one seal or sync runs at a time.
func (p Paths) Lock() (unlock func(), err error) { return p.LockFor("") }

// holderNames describe a lock holder to the user.
var holderNames = map[string]string{
	"autosync":              "后台自动同步到备份位置",
	"terminal-action":       "终端管理命令",
	"native":                "命令行与向导",
	"service_control":       "服务启停控制",
	"instance_write":        "写入实例档案",
	"instance_forget":       "移除实例记录",
	"instance_reactivate":   "重新启用实例",
	"entry_prepare":         "入口准备",
	"entry_port_set":        "修改入口端口",
	"agent_label_set":       "修改实例备注",
	"stage_begin":           "准备恢复暂存目录",
	"stage_verify":          "校验恢复暂存目录",
	"swap":                  "原子目录交换",
	"revive_resume":         "继续恢复流程",
	"revive_review":         "核对恢复状态",
	"revive_resume_checked": "确认后继续恢复",
	"kit_install":           "安装代理工具包",
	"kit_uninstall":         "卸载代理工具包",
	"kit_tool_begin":        "安装辅助工具",
	"kit_tool_remove":       "移除辅助工具",
	"proposals_decide":      "应用分类变更",
	"login_prepare":         "准备登录中继",
	"login_end":             "结束登录中继",
	"prev_drop":             "清理恢复前目录",
	"old_cleanup":           "清理旧目录",
	"seal":                  "建立备份",
	"sync":                  "同步到备份位置",
	"protection":            "更新保护计划",
	"stage-write":           "写入恢复暂存文件",
	"tool-put":              "上传辅助工具",
	"action":                "管理动作",
}

type lockInfo struct {
	Holder    string `json:"holder,omitempty"`
	PID       int    `json:"pid,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

func parseLockInfo(b []byte) lockInfo {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return lockInfo{}
	}
	if strings.HasPrefix(s, "{") {
		var info lockInfo
		if json.Unmarshal([]byte(s), &info) == nil {
			return info
		}
	}
	// Legacy plain-text holder format.
	return lockInfo{Holder: s}
}

func describeLockHolder(holder string) string {
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return "其他 Valise 操作"
	}
	if name, ok := holderNames[holder]; ok {
		return name
	}
	if strings.HasPrefix(holder, "action:") {
		if id := strings.TrimPrefix(holder, "action:"); id != "" {
			return "管理动作 " + id
		}
		return "管理动作"
	}
	return holder
}

func formatBusyReason(info lockInfo) string {
	name := describeLockHolder(info.Holder)
	var meta []string
	if info.PID > 0 {
		meta = append(meta, fmt.Sprintf("PID %d", info.PID))
	}
	if info.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, info.StartedAt); err == nil {
			meta = append(meta, "始于 "+t.Local().Format("15:04"))
		}
	}
	if len(meta) > 0 {
		return fmt.Sprintf("服务器正被「%s」(%s) 占用", name, strings.Join(meta, "，"))
	}
	return fmt.Sprintf("服务器正被「%s」占用", name)
}

// LockFor takes the vault lock and records what holds it, so a refused
// caller can say what is running.
func (p Paths) LockFor(holder string) (unlock func(), err error) {
	_, unlock, err = p.acquire(holder, "")
	return unlock, err
}

// ActionLock uses the same mutation lock; only the named pending action is exempt.
// A child command inherits the descriptor so recovery cannot race a surviving child.
func (p Paths) ActionLock(op string) (*os.File, func(), error) {
	return p.acquire("action:"+op, op)
}

func (p Paths) acquire(holder, except string) (*os.File, func(), error) {
	if err := os.MkdirAll(p.State(), 0o700); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(filepath.Join(p.State(), "vault.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			var info lockInfo
			if b, rerr := os.ReadFile(f.Name()); rerr == nil {
				info = parseLockInfo(b)
			}
			f.Close()
			return nil, nil, valise.New("vault_busy", formatBusyReason(info), "等待该操作完成，或在服务器终端检查并结束该进程")
		}
		f.Close()
		return nil, nil, err
	}
	if err := (actionjournal.Store{Home: p.Home}).Check(except); err != nil {
		f.Close()
		return nil, nil, err
	}
	myPID := os.Getpid()
	payload, _ := json.Marshal(lockInfo{
		Holder:    holder,
		PID:       myPID,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	})
	_ = f.Truncate(0)
	_, _ = f.WriteAt(payload, 0)
	return f, func() {
		// Only clear lock metadata if it still belongs to this process (or has
		// no PID), so an inherited child that updated vault.lock keeps its info.
		if b, rerr := os.ReadFile(f.Name()); rerr == nil {
			cur := parseLockInfo(b)
			if cur.PID == 0 || cur.PID == myPID {
				_ = f.Truncate(0)
			}
		}
		// Close releases this reference; an inherited child keeps its lock.
		_ = f.Close()
	}, nil
}

// AuditEntry is one line of audit.jsonl. It never holds file contents or
// credentials (docs/ARCHITECTURE.md section 10).
type AuditEntry struct {
	Time        time.Time `json:"time"`
	Op          string    `json:"op"`
	Instance    string    `json:"instance,omitempty"`
	Initiator   string    `json:"initiator"`
	Result      string    `json:"result"`
	SnapshotID  string    `json:"snapshot_id,omitempty"`
	Bytes       int64     `json:"bytes,omitempty"`
	Target      string    `json:"target,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Tier        string    `json:"tier,omitempty"`
	OperationID string    `json:"operation_id,omitempty"`
}

// Audit appends one entry.
func (p Paths) AppendAudit(e AuditEntry) {
	if err := os.MkdirAll(p.State(), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(p.Audit(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	fmt.Fprintf(f, "%s\n", b)
}

// ResolveRclone prefers the Kit-installed rclone and falls back to the
// server's login PATH.
func (p Paths) ResolveRclone(e *porterenroll.Env) (string, error) {
	if st, err := os.Stat(p.Rclone()); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
		return p.Rclone(), nil
	}
	if e != nil {
		if path, err := e.LookPath("rclone"); err == nil {
			return path, nil
		}
	}
	return "", valise.New("rclone_missing", "服务器上没有找到 rclone", "在服务器设置或云盘登录页安装 rclone，或在服务器命令行自行安装后再试")
}
