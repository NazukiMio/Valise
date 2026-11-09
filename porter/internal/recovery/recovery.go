// Package recovery is porter's side of finding the Valise passphrase
// again (docs/ARCHITECTURE.md 3.2): requests the client places in the
// user's own staging folder, and `recovery-code`, which the user runs with
// sudo. As root it only reads the SSH host key; it then becomes the user
// before touching anything in the user's home.
package recovery

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"filippo.io/age"
	"filippo.io/age/agessh"

	core "valise/core/recovery"
	"valise/core/valise"
)

// HostKeyPath is the host key the backup lock is sealed to.
const HostKeyPath = "/etc/ssh/ssh_host_ed25519_key"

const maxFile = 1 << 16

func dir(home string) string { return filepath.Join(home, core.Dir) }

func reqPath(home, id string) string { return filepath.Join(dir(home), id+".request") }
func resPath(home, id string) string { return filepath.Join(dir(home), id+".result") }

type request struct {
	Lock      []byte `json:"lock"`
	Recipient string `json:"recipient"`
}

// Place writes a request; any older request is dropped.
func Place(home string, p core.RequestParams) error {
	if !core.ValidID(p.ID) || len(p.Lock) == 0 || len(p.Lock) > maxFile {
		return valise.New("recovery_invalid", "找回请求无效", "重新开始找回")
	}
	if _, err := age.ParseX25519Recipient(p.Recipient); err != nil {
		return valise.New("recovery_invalid", "找回请求无效", "重新开始找回")
	}
	if err := os.MkdirAll(dir(home), 0o700); err != nil {
		return err
	}
	clearAll(home)
	b := encode(request{Lock: p.Lock, Recipient: p.Recipient})
	return writeNew(reqPath(home, p.ID), b)
}

// Result reads the answer, or reports that the command has not run yet.
func Result(home, id string) ([]byte, error) {
	if !core.ValidID(id) {
		return nil, valise.New("recovery_invalid", "找回请求无效", "重新开始找回")
	}
	b, err := readNoFollow(resPath(home, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, valise.New(core.WaitingCode, "服务器上还没有找回结果", "在服务器上运行那条 sudo 命令后再继续")
	}
	return b, err
}

// Clear removes a request and its answer.
func Clear(home, id string) error {
	if !core.ValidID(id) {
		return valise.New("recovery_invalid", "找回请求无效", "重新开始找回")
	}
	for _, p := range []string{reqPath(home, id), resPath(home, id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func clearAll(home string) {
	entries, _ := os.ReadDir(dir(home))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".request") || strings.HasSuffix(e.Name(), ".result") {
			_ = os.Remove(filepath.Join(dir(home), e.Name()))
		}
	}
}

// Answer opens the newest request in home with the host key and seals R
// to the request's one-time recipient. It returns the verification code.
// It runs as the user, never as root.
func Answer(hostKey []byte, home string) (string, error) {
	entries, err := os.ReadDir(dir(home))
	if err != nil {
		return "", errors.New("没有找到找回请求：先在 Valise 里开始找回")
	}
	var ids []string
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".request"); ok && core.ValidID(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 {
		sort.Strings(ids)
		return "", fmt.Errorf("应当正好有一个找回请求，现在有 %d 个：在 Valise 里重新开始找回", len(ids))
	}
	id := ids[0]
	b, err := readNoFollow(reqPath(home, id))
	if err != nil {
		return "", err
	}
	var req request
	if err := decode(b, &req); err != nil {
		return "", errors.New("找回请求无法读取：在 Valise 里重新开始找回")
	}
	ident, err := agessh.ParseIdentity(hostKey)
	if err != nil {
		return "", errors.New("无法读取主机密钥 " + HostKeyPath)
	}
	r, err := age.Decrypt(bytes.NewReader(req.Lock), ident)
	if err != nil {
		return "", errors.New("这把备用锁不是用这台服务器现在的主机密钥做的（主机密钥可能变过）：换一台服务器找回")
	}
	plain, err := io.ReadAll(io.LimitReader(r, maxFile))
	if err != nil {
		return "", err
	}
	defer zero(plain)
	to, err := age.ParseX25519Recipient(req.Recipient)
	if err != nil {
		return "", errors.New("找回请求无效：在 Valise 里重新开始找回")
	}
	var out bytes.Buffer
	w, err := age.Encrypt(&out, to)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(plain); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	if err := writeNew(resPath(home, id), out.Bytes()); err != nil {
		return "", err
	}
	return core.Code(id, out.Bytes()), nil
}

// Run is `recovery-code --for <user>`: read the host key as root, become
// the user (who must be the one who ran sudo), then answer.
func Run(forUser string) (string, error) {
	if os.Geteuid() != 0 {
		return "", errors.New("请用 sudo 运行")
	}
	if forUser == "" || os.Getenv("SUDO_USER") != forUser {
		return "", errors.New("--for 必须是运行 sudo 的那个账号")
	}
	u, err := user.Lookup(forUser)
	if err != nil {
		return "", err
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil || uid == 0 {
		return "", errors.New("不能为 root 账号找回")
	}
	key, err := os.ReadFile(HostKeyPath)
	if err != nil {
		return "", errors.New("无法读取主机密钥 " + HostKeyPath)
	}
	defer zero(key)
	if err := dropTo(uid, gid); err != nil {
		zero(key)
		return "", err
	}
	return Answer(key, u.HomeDir)
}

func dropTo(uid, gid int) error {
	if err := syscall.Setgroups(nil); err != nil {
		return err
	}
	if err := syscall.Setgid(gid); err != nil {
		return err
	}
	if err := syscall.Setuid(uid); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return errors.New("没能放下 root 权限")
	}
	return nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
