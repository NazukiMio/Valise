package kit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"valise/porter/internal/runtimeguard"

	"valise/core/agentkit"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/install"
	"valise/porter/internal/timer"
)

// Enhanced Kit tools (docs/AGENT_KIT.md 8.4.1): official binaries the
// client downloaded and checked, written to ~/.local/bin/<name>. Valise
// never overwrites a file it did not put there, and removes only files it
// put there that are unchanged.

const maxToolBytes = 256 << 20

var (
	toolNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
	toolSHARe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func toolPath(home, name string) string { return filepath.Join(home, ".local", "bin", name) }
func toolRecord(home string) string {
	return filepath.Join(home, ".local", "state", "valise", "kit-tools.json")
}

func loadTools(home string) map[string]string {
	out := map[string]string{}
	if b, err := os.ReadFile(toolRecord(home)); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func saveTools(home string, m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(toolRecord(home)), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(toolRecord(home), append(b, '\n'), 0o600)
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ToolsCheck looks up the Kit tools' programs on the login PATH and in
// ~/.local/bin (nothing is run) and reports linger.
func ToolsCheck(ctx context.Context, e *porterenroll.Env, home string, p agentkit.KitToolsCheckParams) (*agentkit.KitToolsCheckResult, error) {
	if len(p.Binaries) > 32 {
		return nil, valise.New("kit_tool_invalid", "一次最多查 32 个程序", "")
	}
	res := &agentkit.KitToolsCheckResult{Arch: runtime.GOARCH, Found: map[string]string{}, Ours: loadTools(home)}
	res.OSID, _ = install.OSRelease("/etc/os-release")
	for _, b := range p.Binaries {
		if !toolNameRe.MatchString(b) {
			return nil, valise.New("kit_tool_invalid", "程序名无效："+b, "")
		}
		if path, err := e.LookPath(b); err == nil {
			res.Found[b] = path
		} else if st, err := os.Stat(toolPath(home, b)); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			res.Found[b] = toolPath(home, b)
		}
	}
	res.Linger = timer.Linger(ctx, e, home)
	return res, nil
}

// ToolBegin checks that name may be written with content sha.
func ToolBegin(home, name, sha string) error {
	if err := runtimeguard.Check(context.Background(), home, runtimeguard.Scope{Paths: []string{toolPath(home, name)}}); err != nil {
		return err
	}
	if !toolNameRe.MatchString(name) || !toolSHARe.MatchString(sha) {
		return valise.New("kit_tool_invalid", "工具名或 SHA-256 无效", "使用 Valise 资料包里的工具")
	}
	path := toolPath(home, name)
	if _, err := os.Lstat(path); err == nil {
		have, err := fileSHA(path)
		if rec, ok := loadTools(home)[name]; err != nil || !ok || rec != have {
			return valise.New("kit_tool_foreign", path+" 已经存在，不是 Valise 装的（或装好后被改过）", "Valise 不会覆盖它；需要换成 Valise 的版本时，先自己删掉或改名")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ToolPut writes r to ~/.local/bin/name if its SHA-256 is sha.
func ToolPut(home, name, sha string, r io.Reader) error {
	if err := runtimeguard.Check(context.Background(), home, runtimeguard.Scope{Paths: []string{toolPath(home, name)}}); err != nil {
		return err
	}
	if err := ToolBegin(home, name, sha); err != nil {
		return err
	}
	dir := filepath.Dir(toolPath(home, name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b := make([]byte, 6)
	rand.Read(b)
	tmp := filepath.Join(dir, ".valise-tool-"+name+"-"+hex.EncodeToString(b))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, maxToolBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxToolBytes {
		err = valise.New("kit_tool_too_large", "文件太大", "")
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != sha {
		err = valise.New("kit_tool_mismatch", "收到的文件与预期的 SHA-256 不一致，没有安装", "重试")
	}
	if err == nil {
		err = os.Chmod(tmp, 0o755)
	}
	if err == nil {
		err = os.Rename(tmp, toolPath(home, name))
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	m := loadTools(home)
	m[name] = sha
	return saveTools(home, m)
}

// ToolRemove deletes ~/.local/bin/name when Valise put it there and it is
// unchanged. It runs only on the user's explicit command.
func ToolRemove(home, name string) error {
	if err := runtimeguard.Check(context.Background(), home, runtimeguard.Scope{Paths: []string{toolPath(home, name)}}); err != nil {
		return err
	}
	if !toolNameRe.MatchString(name) {
		return valise.New("kit_tool_invalid", "工具名无效", "")
	}
	m := loadTools(home)
	rec, ok := m[name]
	if !ok {
		return valise.New("kit_tool_not_ours", toolPath(home, name)+" 不是 Valise 装的", "Valise 只删除它自己装的文件")
	}
	path := toolPath(home, name)
	if have, err := fileSHA(path); err == nil && have != rec {
		return valise.New("kit_tool_changed", path+" 装好后被改过，Valise 不删除它", "需要时自己删除")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	delete(m, name)
	return saveTools(home, m)
}
