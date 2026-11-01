package service

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"valise/core/valise"
)

// Secret output goes to the clipboard and is cleared after SecretWindow if
// it is still there (docs/INSTALL.md section 5).

// SecretWindow is how long a copied secret stays on the clipboard.
const SecretWindow = 20 * time.Second

// clipboard tools and the window; tests replace them.
var (
	secretWindow   = SecretWindow
	clipboardCopy  = systemCopy
	clipboardPaste = systemPaste
)

func clipboardTools() (copyArgs, pasteArgs []string) {
	if runtime.GOOS == "darwin" {
		return []string{"pbcopy"}, []string{"pbpaste"}
	}
	for _, c := range [][2][]string{
		{{"wl-copy"}, {"wl-paste", "--no-newline"}},
		{{"xclip", "-selection", "clipboard"}, {"xclip", "-selection", "clipboard", "-o"}},
		{{"xsel", "--clipboard", "--input"}, {"xsel", "--clipboard", "--output"}},
	} {
		if _, err := exec.LookPath(c[0][0]); err == nil {
			return c[0], c[1]
		}
	}
	return nil, nil
}

func systemCopy(s string) error {
	args, _ := clipboardTools()
	if args == nil {
		return valise.New("clipboard_missing", "找不到剪贴板工具", "macOS 自带 pbcopy；Linux 请安装 wl-clipboard、xclip 或 xsel")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

func systemPaste() (string, error) {
	_, args := clipboardTools()
	if args == nil {
		return "", valise.New("clipboard_missing", "找不到剪贴板工具", "")
	}
	out, err := exec.Command(args[0], args[1:]...).Output()
	return string(out), err
}

// SecretClip is a secret on the clipboard.
type SecretClip struct {
	secret string
	done   chan struct{}
	cancel context.CancelFunc
}

// CopySecret puts secret on the clipboard and clears it after SecretWindow
// (or Clear), unless the user copied something else meanwhile.
func CopySecret(ctx context.Context, secret string) (*SecretClip, error) {
	if err := clipboardCopy(secret); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &SecretClip{secret: secret, done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(c.done)
		select {
		case <-ctx.Done():
		case <-time.After(secretWindow):
		}
		c.clear()
	}()
	return c, nil
}

func (c *SecretClip) clear() {
	if cur, err := clipboardPaste(); err == nil && strings.TrimRight(cur, "\n") == c.secret {
		_ = clipboardCopy("")
	}
}

// Clear clears the clipboard now (if it still holds the secret) and waits.
func (c *SecretClip) Clear() {
	c.cancel()
	<-c.done
}

// Done is closed once the clipboard has been cleared.
func (c *SecretClip) Done() <-chan struct{} { return c.done }
