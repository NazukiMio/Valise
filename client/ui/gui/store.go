package gui

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

// The desktop keeps a little state of its own on this computer, beside
// Valise's data (docs/UI.md 7.3): the home page's view, the reminders the
// person has acknowledged, and the pictures chosen for agents. None of it
// leaves this computer.

type prefs struct {
	View string `json:"view"` // "cards" (default) or "list"
	// Dismissed maps a reminder's key to the state it was acknowledged in;
	// a different state shows the reminder again.
	Dismissed map[string]string `json:"dismissed"`
}

func (a *App) guiDir() string { return filepath.Join(a.svc.Dirs.Data, "desktop") }

func (a *App) loadPrefs() {
	a.prefs = prefs{View: "cards", Dismissed: map[string]string{}}
	b, err := os.ReadFile(filepath.Join(a.guiDir(), "prefs.json"))
	if err == nil {
		json.Unmarshal(b, &a.prefs)
	}
	if a.prefs.Dismissed == nil {
		a.prefs.Dismissed = map[string]string{}
	}
}

// savePrefs writes through a temporary file so a crash never leaves half.
func (a *App) savePrefs() {
	if err := os.MkdirAll(a.guiDir(), 0o700); err != nil {
		return
	}
	b, _ := json.MarshalIndent(a.prefs, "", "  ")
	tmp := filepath.Join(a.guiDir(), ".prefs.json.tmp")
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, filepath.Join(a.guiDir(), "prefs.json"))
	}
}

// MaxAgentImage bounds a chosen picture.
const MaxAgentImage = 4 << 20

func (a *App) imagePath(server, instance string) string {
	return filepath.Join(a.guiDir(), "agent-images", url.PathEscape(server)+"__"+url.PathEscape(instance))
}

// setAgentImage copies a chosen file into the desktop's folder after
// checking that it is a picture the window can show.
func (a *App) setAgentImage(server, instance, src string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if st.Size() > MaxAgentImage {
		return errors.New("图片超过 4 MiB，请换一张小一些的")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	img, err := ui.DecodeImage(b)
	if err != nil {
		return errors.New("这个文件不是能显示的图片（支持 PNG、JPEG、WebP、SVG）")
	}
	dst := a.imagePath(server, instance)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(dst+".tmp", b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(dst+".tmp", dst); err != nil {
		return err
	}
	a.images[server+"/"+instance] = img
	return nil
}

// clearAgentImage goes back to the official logo or the robot. The copy is
// moved aside, not deleted (AGENTS.md 4: no deletion without a command).
func (a *App) clearAgentImage(server, instance string) {
	p := a.imagePath(server, instance)
	if _, err := os.Stat(p); err == nil {
		os.Rename(p, p+".removed")
	}
	a.images[server+"/"+instance] = nil
}

// agentImage is the chosen picture, decoded once.
func (a *App) agentImage(server, instance string) ui.ImageSource {
	key := server + "/" + instance
	if img, ok := a.images[key]; ok {
		return img
	}
	var img ui.ImageSource
	if b, err := os.ReadFile(a.imagePath(server, instance)); err == nil && !strings.HasSuffix(key, "/") {
		img, _ = ui.DecodeImage(b)
	}
	a.images[key] = img
	return img
}
