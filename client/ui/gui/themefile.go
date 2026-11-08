package gui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Theme files (docs/UI.md 7.4): a folder with theme.json and the pictures
// it names. A theme is data only; it is read from this computer, never from
// the network.

// Built-in theme ids, and the choice that follows the system.
const (
	ThemeSystem = "system"
	ThemeLight  = "classic-light"
	ThemeDark   = "classic-dark"
	ThemeFormat = "valise-theme/1"
)

// Limits of a theme folder.
const (
	MaxThemeJSON   = 256 << 10
	MaxThemeImage  = 4 << 20
	MaxThemeFolder = 16 << 20
)

// themeFile is theme.json as written.
type themeFile struct {
	Format   string            `json:"format"`
	Name     string            `json:"name"`
	Base     string            `json:"base"`
	Dark     *bool             `json:"dark"`
	Colors   map[string]string `json:"colors"`
	Fonts    *Fonts            `json:"fonts"`
	Sizes    *Sizes            `json:"sizes"`
	Variants struct {
		Timeline string `json:"timeline"`
	} `json:"variants"`
	Images map[string]string `json:"images"`
	Layout struct {
		Shell *Node `json:"shell"`
		Home  *Node `json:"home"`
	} `json:"layout"`
}

var (
	colorRe = regexp.MustCompile(`^#([0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	nameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	fontRe  = regexp.MustCompile(`^[\p{L}\p{N} ,._-]{0,200}$`)
)

// readTheme reads and checks a theme folder. The error says which item is
// wrong and how to fix it.
func readTheme(dir string) (*Theme, []string, error) {
	b, err := readLimited(filepath.Join(dir, "theme.json"), MaxThemeJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("读不到 theme.json：%w", err)
	}
	var f themeFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, nil, fmt.Errorf("theme.json 格式不对：%v", err)
	}
	if f.Format != ThemeFormat {
		return nil, nil, fmt.Errorf("format 要写 %q", ThemeFormat)
	}
	if strings.TrimSpace(f.Name) == "" || len([]rune(f.Name)) > 40 {
		return nil, nil, errors.New("name 要写主题的名字（40 字以内）")
	}
	var t *Theme
	switch f.Base {
	case "", ThemeLight:
		t = classicLight()
	case ThemeDark:
		t = classicDark()
	default:
		return nil, nil, fmt.Errorf("base 只能是 %q 或 %q", ThemeLight, ThemeDark)
	}
	t.Name = f.Name
	if f.Dark != nil {
		t.Dark = *f.Dark
	}
	fields := colorFields(&t.Pal)
	for k, v := range f.Colors {
		dst := fields[k]
		if dst == nil {
			return nil, nil, fmt.Errorf("colors 里没有叫 %q 的颜色", k)
		}
		if !colorRe.MatchString(v) {
			return nil, nil, fmt.Errorf("颜色 %s 要写成 #rrggbb 或 #rrggbbaa，现在是 %q", k, v)
		}
		*dst = ui.Hex(v)
	}
	if f.Fonts != nil {
		for _, s := range []*string{&f.Fonts.Text, &f.Fonts.Display, &f.Fonts.Mono} {
			if !fontRe.MatchString(*s) {
				return nil, nil, fmt.Errorf("字体名只能用文字、数字、空格和逗号：%q", *s)
			}
		}
		merge(&t.Fonts.Text, f.Fonts.Text)
		merge(&t.Fonts.Display, f.Fonts.Display)
		merge(&t.Fonts.Mono, f.Fonts.Mono)
	}
	if s := f.Sizes; s != nil {
		for _, v := range []float32{s.Radius, s.CardRadius, s.Verdict, s.Title, s.Text} {
			if v < 0 || v > 120 {
				return nil, nil, errors.New("sizes 里的数要在 0 到 120 之间（0 表示沿用）")
			}
		}
		mergeNum(&t.Sizes.Radius, s.Radius)
		mergeNum(&t.Sizes.CardRadius, s.CardRadius)
		mergeNum(&t.Sizes.Verdict, s.Verdict)
		mergeNum(&t.Sizes.Title, s.Title)
		mergeNum(&t.Sizes.Text, s.Text)
	}
	switch f.Variants.Timeline {
	case "":
	case TimelineStrands, TimelineGold, TimelinePlain:
		t.Timeline = f.Variants.Timeline
	default:
		return nil, nil, fmt.Errorf("variants.timeline 只能是 %s、%s 或 %s", TimelineStrands, TimelineGold, TimelinePlain)
	}
	files, err := themeImages(dir, f.Images)
	if err != nil {
		return nil, nil, err
	}
	t.Images = map[string]ui.ImageSource{}
	names := map[string]bool{}
	for name, b := range files {
		img, err := ui.DecodeImage(b)
		if err != nil {
			return nil, nil, fmt.Errorf("图片 %s（%s）不是能显示的图片（支持 PNG、JPEG、WebP、SVG）", name, f.Images[name])
		}
		t.Images[name], names[name] = img, true
	}
	if f.Layout.Shell != nil {
		if err := shellSlots.check(f.Layout.Shell, names); err != nil {
			return nil, nil, err
		}
		t.Shell = f.Layout.Shell
	}
	if f.Layout.Home != nil {
		if err := homeSlots.check(f.Layout.Home, names); err != nil {
			return nil, nil, err
		}
		t.Home = f.Layout.Home
	}
	t.Notes = append(shellSlots.complete(t.Shell), homeSlots.complete(t.Home)...)
	return t, sortedFiles(f.Images), nil
}

// themeImages reads the pictures a theme names: inside its folder only,
// each and all within their limits.
func themeImages(dir string, images map[string]string) (map[string][]byte, error) {
	out := map[string][]byte{}
	total := int64(0)
	for name, rel := range images {
		if !nameRe.MatchString(name) {
			return nil, fmt.Errorf("图片名 %q 只能用小写字母、数字和连字符", name)
		}
		clean := filepath.Clean(filepath.FromSlash(rel))
		if rel == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("图片 %s 要写主题文件夹里的相对路径，现在是 %q", name, rel)
		}
		p := filepath.Join(dir, clean)
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() {
			return nil, fmt.Errorf("主题文件夹里找不到图片文件 %s", rel)
		}
		if st.Size() > MaxThemeImage {
			return nil, fmt.Errorf("图片 %s 超过 4 MiB", rel)
		}
		if total += st.Size(); total > MaxThemeFolder {
			return nil, errors.New("主题的图片加起来超过 16 MiB")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out[name] = b
	}
	return out, nil
}

func sortedFiles(images map[string]string) []string {
	var out []string
	for _, rel := range images {
		out = append(out, filepath.Clean(filepath.FromSlash(rel)))
	}
	sort.Strings(out)
	return out
}

func readLimited(p string, limit int64) ([]byte, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Size() > limit {
		return nil, fmt.Errorf("文件超过 %d KiB", limit>>10)
	}
	return os.ReadFile(p)
}

func merge(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func mergeNum(dst *float32, v float32) {
	if v > 0 {
		*dst = v
	}
}

func (a *App) themesDir() string { return filepath.Join(a.guiDir(), "themes") }

// installedThemes are the imported theme folders, by id, in name order.
func (a *App) installedThemes() []string {
	entries, _ := os.ReadDir(a.themesDir())
	var out []string
	for _, e := range entries {
		if e.IsDir() && nameRe.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// importTheme checks a chosen folder, then copies theme.json and the
// pictures it names (nothing else) into the desktop's themes folder.
func (a *App) importTheme(src string) (string, error) {
	_, files, err := readTheme(src)
	if err != nil {
		return "", err
	}
	id := themeID(filepath.Base(src))
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(a.themesDir(), id)); errors.Is(err, os.ErrNotExist) {
			break
		}
		id = themeID(filepath.Base(src)) + fmt.Sprint("-", i)
	}
	tmp := filepath.Join(a.themesDir(), "."+id+".tmp")
	for _, rel := range append([]string{"theme.json"}, files...) {
		b, err := os.ReadFile(filepath.Join(src, rel))
		if err == nil {
			err = os.MkdirAll(filepath.Dir(filepath.Join(tmp, rel)), 0o700)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(tmp, rel), b, 0o600)
		}
		if err != nil {
			return "", fmt.Errorf("复制主题失败：%w", err)
		}
	}
	if err := os.Rename(tmp, filepath.Join(a.themesDir(), id)); err != nil {
		return "", fmt.Errorf("复制主题失败：%w", err)
	}
	return id, nil
}

// themeID makes a folder name into an id.
func themeID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	if len(id) > 24 {
		id = id[:24]
	}
	if id == "" || id == ThemeLight || id == ThemeDark || id == ThemeSystem {
		id = "theme"
	}
	return id
}
