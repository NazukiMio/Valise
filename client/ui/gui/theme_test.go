package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// writeTheme makes a theme folder with theme.json and a small picture.
func writeTheme(t *testing.T, json string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "My Immersive")
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o700); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{200, 100, 50, 255})
	var b bytes.Buffer
	png.Encode(&b, img)
	os.WriteFile(filepath.Join(dir, "img", "bg.png"), b.Bytes(), 0o600)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not copied"), 0o600)
	os.WriteFile(filepath.Join(dir, "theme.json"), []byte(json), 0o600)
	return dir
}

const immersive = `{
  "format": "valise-theme/1", "name": "沉浸", "base": "classic-dark",
  "colors": {"paper": "#101820", "warn": "#ffb347"},
  "fonts": {"display": "Georgia"},
  "sizes": {"verdict": 44},
  "variants": {"timeline": "gold"},
  "images": {"backdrop": "img/bg.png"},
  "layout": {
    "shell": {"row": [{"slot": "nav", "style": "rail", "width": 64, "padding": [20, 8]}, {"slot": "page", "grow": 1, "padding": [24]}]},
    "home": {"column": [{"slot": "verdict"}, {"slot": "agents", "style": "large", "columns": 1}], "gap": 28}
  }
}`

// An imported theme changes tokens, variants and layout; Valise copies only
// what the theme names, and puts back what must always show (UI.md 7.4).
func TestImportImmersiveTheme(t *testing.T) {
	a, tt := world(t)
	a.pickFolder = func() (string, error) { return writeTheme(t, immersive), nil }
	a.importChosen()
	if a.themeErr != nil || a.prefs.Theme != "my-immersive" {
		t.Fatalf("import: %v %q", a.themeErr, a.prefs.Theme)
	}
	tt.Frame()
	tt.Advance(time.Second)
	if cur.Name != "沉浸" || !cur.Dark || cur.Timeline != TimelineGold || cur.Sizes.Verdict != 44 || cur.Images["backdrop"] == nil {
		t.Fatalf("theme not in use: %+v", cur)
	}
	var copied []string
	filepath.Walk(filepath.Join(a.themesDir(), "my-immersive"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(filepath.Join(a.themesDir(), "my-immersive"), p)
			copied = append(copied, rel)
		}
		return nil
	})
	sort.Strings(copied)
	if strings.Join(copied, ",") != filepath.Join("img", "bg.png")+",theme.json" {
		t.Fatalf("copied %v", copied)
	}
	// The reminders and the theme menu were left out: they are put back.
	for _, want := range []string{"nas 这次没有读到", "带我去处理", "主题 · 沉浸", "首页缺少“待处理提醒”，已按经典主题补上",
		"外壳缺少“主题菜单”，已按经典主题补上", "2 个 Agent，2 件事需要你处理"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// Left out on purpose: the search and the tagline.
	if tt.HasText("搜索 Agent") || tt.HasText("带着你的 Agent 去旅行。") {
		t.Errorf("slots the theme left out drawn: %q", tt.Texts())
	}
	// The rail still leads everywhere.
	click(t, tt, "服务器")
	if a.router.Path() != "/servers" {
		t.Fatalf("rail went to %s", a.router.Path())
	}
	// A second app over the same data reads the theme back.
	b := New(a.svc)
	b.loadPrefs()
	b.loadThemes()
	b.chooseTheme(false)
	if cur.Name != "沉浸" {
		t.Fatalf("not kept: %q", cur.Name)
	}
}

// A theme with a mistake is not imported, and says what is wrong.
func TestThemeMistakes(t *testing.T) {
	cases := map[string]string{
		`{"format":"valise-theme/1","name":"x","script":"alert(1)"}`:                                                  "格式不对",
		`{"format":"valise-theme/1","name":"x","colors":{"paper":"red"}}`:                                             "#rrggbb",
		`{"format":"valise-theme/1","name":"x","colors":{"glow":"#ffffff"}}`:                                          "没有叫",
		`{"format":"valise-theme/1","name":"x","images":{"bg":"../../etc/passwd"}}`:                                   "相对路径",
		`{"format":"valise-theme/1","name":"x","images":{"bg":"img/none.png"}}`:                                       "找不到",
		`{"format":"valise-theme/1","name":"x","images":{"bg":"theme.json"}}`:                                         "不是能显示的图片",
		`{"format":"valise-theme/1","name":"x","layout":{"home":{"slot":"settings"}}}`:                                "没有叫 \"settings\" 的插槽",
		`{"format":"valise-theme/1","name":"x","layout":{"home":{"column":[{"slot":"verdict"},{"slot":"verdict"}]}}}`: "两次",
		`{"format":"valise-theme/1","name":"x","layout":{"shell":{"slot":"nav","style":"floating"}}}`:                 "没有样式",
		`{"format":"valise-theme/1","name":"x","layout":{"home":{"row":[{"slot":"note"}],"slot":"verdict"}}}`:         "恰好一种",
		`{"format":"valise-theme/1","name":"x","variants":{"timeline":"fireworks"}}`:                                  "variants.timeline",
		`{"format":"valise-theme/2","name":"x"}`:                                                                      "format",
	}
	for json, want := range cases {
		_, _, err := readTheme(writeTheme(t, json))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", json, err, want)
		}
	}
	deep := `{"slot":"verdict"}`
	for range maxDepth + 1 {
		deep = `{"column":[` + deep + `]}`
	}
	if _, _, err := readTheme(writeTheme(t, `{"format":"valise-theme/1","name":"x","layout":{"home":`+deep+`}}`)); err == nil {
		t.Error("a tree deeper than the limit was accepted")
	}
}

// A theme that breaks after import falls back to following the system and
// says why; the built-ins follow the system or stay as chosen.
func TestThemeFallbackAndBuiltins(t *testing.T) {
	a, tt := world(t)
	tt.SetDark(true)
	if !cur.Dark || cur.ID != ThemeDark {
		t.Fatalf("not following the system: %q", cur.ID)
	}
	a.setTheme(ThemeLight)
	tt.Frame()
	if cur.ID != ThemeLight {
		t.Fatalf("light not kept: %q", cur.ID)
	}
	dir := filepath.Join(a.themesDir(), "broken")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"format":"valise-theme/1"}`), 0o600)
	a.loadThemes()
	a.setTheme("broken")
	tt.Frame()
	if cur.ID != ThemeDark || !tt.HasText("主题“broken”不能使用：name 要写主题的名字（40 字以内）。已改用跟随系统") {
		t.Fatalf("no fallback: %q %q", cur.ID, tt.Texts())
	}
	tt.SetDark(false)
}

// The example in docs/UI.md 7.4.2 is a theme Valise accepts as written.
func TestDocumentedThemeExample(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "UI.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(b), "示例（沉浸")
	_, rest, ok2 := strings.Cut(rest, "```json\n")
	example, _, ok3 := strings.Cut(rest, "```")
	if !ok || !ok2 || !ok3 {
		t.Fatal("example not found in UI.md 7.4.2")
	}
	th, _, err := readTheme(writeTheme(t, example))
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Notes) != 0 || th.Shell.find("tagline") || !th.Home.find("reminders") {
		t.Fatalf("example: notes %v", th.Notes)
	}
}
