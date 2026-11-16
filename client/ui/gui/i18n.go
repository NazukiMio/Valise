package gui

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// The interface's words (docs/UI.md 7.6): every sentence is a standard
// English key; the current catalog gives its text. The built-in catalog is
// the reference: its keys are all the keys, and its placeholders are each
// key's placeholders.

//go:embed i18n/zh-CN.json
var builtinCatalog []byte

// LocaleFormat is a catalog file's format.
const LocaleFormat = "valise-locale/1"

// Limits of an imported catalog.
const (
	MaxLocaleFile    = 1 << 20
	MaxLocaleMessage = 2000
)

// message is one key's text, with an optional singular form for {n}.
type message struct{ One, Other string }

func (m *message) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &m.Other)
	}
	var f struct {
		One   string `json:"one"`
		Other string `json:"other"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return err
	}
	if f.Other == "" {
		return errors.New(T("lang.err.plural"))
	}
	m.One, m.Other = f.One, f.Other
	return nil
}

// catalog is one language's words.
type catalog struct {
	ID, Lang, Name string
	Msgs           map[string]message
	Missing        int  // keys the catalog lacks, shown in the built-in language
	Imported       bool // not reviewed by Valise
}

type localeFile struct {
	Format   string             `json:"format"`
	Lang     string             `json:"lang"`
	Name     string             `json:"name"`
	Messages map[string]message `json:"messages"`
}

// baseCatalog is set once at init and never changed. words is the current
// catalog: T reads it from any goroutine while chooseLocale swaps it on the
// main thread, so it is only loaded and stored atomically.
var (
	baseCatalog *catalog
	words       atomic.Pointer[catalog]
)

func init() {
	c, err := parseCatalog(builtinCatalog, nil)
	if err != nil {
		panic("built-in catalog: " + err.Error())
	}
	c.ID = c.Lang
	baseCatalog = c
	words.Store(c)
}

var (
	placeholderRe = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)
	langRe        = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
	keyRe         = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+$`)
)

// T is the text of key in the current language, with its {name}
// placeholders filled from args given as name, value pairs. A key the
// catalog lacks shows as itself, so a missing word is seen, not hidden.
func T(key string, args ...any) string {
	w := words.Load()
	if w == nil { // still reading the built-in catalog
		return key
	}
	m, ok := w.Msgs[key]
	if !ok && baseCatalog != nil {
		if m, ok = baseCatalog.Msgs[key]; !ok {
			return key
		}
	}
	text := m.Other
	vals := map[string]string{}
	for i := 0; i+1 < len(args); i += 2 {
		name, _ := args[i].(string)
		vals[name] = fmt.Sprint(args[i+1])
		if name == "n" && m.One != "" && fmt.Sprint(args[i+1]) == "1" {
			text = m.One
		}
	}
	return placeholderRe.ReplaceAllStringFunc(text, func(p string) string {
		if v, ok := vals[p[1:len(p)-1]]; ok {
			return v
		}
		return p
	})
}

func placeholders(s string) string {
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// parseCatalog reads and checks a catalog; against base, when given, every
// key must be known and keep the base's placeholders. The error says which
// entry is wrong and how to fix it.
func parseCatalog(b []byte, base *catalog) (*catalog, error) {
	var f localeFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, errors.New(T("lang.err.format_json", "reason", err.Error()))
	}
	switch {
	case f.Format != LocaleFormat:
		return nil, errors.New(T("lang.err.format", "format", LocaleFormat))
	case !langRe.MatchString(f.Lang):
		return nil, errors.New(T("lang.err.lang", "value", f.Lang))
	case strings.TrimSpace(f.Name) == "" || utf8.RuneCountInString(f.Name) > 40:
		return nil, errors.New(T("lang.err.name"))
	}
	c := &catalog{ID: f.Lang, Lang: f.Lang, Name: f.Name, Msgs: f.Messages}
	for key, m := range f.Messages {
		if !keyRe.MatchString(key) {
			return nil, errors.New(T("lang.err.key", "key", key))
		}
		for _, s := range []string{m.One, m.Other} {
			if utf8.RuneCountInString(s) > MaxLocaleMessage {
				return nil, errors.New(T("lang.err.long", "key", key, "n", MaxLocaleMessage))
			}
			if strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) >= 0 {
				return nil, errors.New(T("lang.err.control", "key", key))
			}
		}
		if base == nil {
			continue
		}
		ref, ok := base.Msgs[key]
		if !ok {
			return nil, errors.New(T("lang.err.unknown_key", "key", key))
		}
		want := placeholders(ref.Other)
		for _, s := range []string{m.One, m.Other} {
			if s != "" && placeholders(s) != want {
				return nil, errors.New(T("lang.err.placeholders", "key", key, "want", "{"+strings.ReplaceAll(want, ",", "} {")+"}"))
			}
		}
	}
	if base != nil {
		c.Imported = true
		for key := range base.Msgs {
			if _, ok := f.Messages[key]; !ok {
				c.Missing++
			}
		}
	}
	return c, nil
}

func (a *App) localesDir() string { return filepath.Join(a.guiDir(), "locales") }

// loadLocales reads the imported catalogs; one that fails stays listed
// with its reason.
func (a *App) loadLocales() {
	a.locales = map[string]localeEntry{}
	entries, _ := os.ReadDir(a.localesDir())
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || !nameRe.MatchString(id) {
			continue
		}
		c, err := readLocale(filepath.Join(a.localesDir(), e.Name()))
		if c != nil {
			c.ID = id
		}
		a.locales[id] = localeEntry{id: id, cat: c, err: err}
	}
}

type localeEntry struct {
	id  string
	cat *catalog
	err error
}

func readLocale(path string) (*catalog, error) {
	b, err := readLimited(path, MaxLocaleFile)
	if err != nil {
		return nil, errors.New(T("lang.err.read", "reason", err.Error()))
	}
	return parseCatalog(b, baseCatalog)
}

// chooseLocale makes the chosen language current; one that cannot be used
// falls back to the built-in language, and says why.
func (a *App) chooseLocale() {
	a.langErr = nil
	id := a.prefs.Lang
	if id == "" || id == baseCatalog.ID {
		words.Store(baseCatalog)
		return
	}
	e, ok := a.locales[id]
	switch {
	case !ok:
		a.langErr = errors.New(T("lang.err.missing"))
		words.Store(baseCatalog)
	case e.err != nil:
		a.langErr = errors.New(T("lang.err.unusable", "id", id, "reason", e.err.Error()))
		words.Store(baseCatalog)
	default:
		words.Store(e.cat)
	}
}

func (a *App) setLocale(id string) {
	a.prefs.Lang = id
	a.savePrefs()
	a.chooseLocale()
}

// importLocale checks a chosen catalog file and copies it into the
// desktop's locales folder.
func (a *App) importLocale(src string) (string, error) {
	c, err := readLocale(src)
	if err != nil {
		return "", err
	}
	id := themeID(c.Lang)
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(a.localesDir(), id+".json")); errors.Is(err, os.ErrNotExist) {
			break
		}
		id = themeID(c.Lang) + fmt.Sprint("-", i)
	}
	b, err := os.ReadFile(src)
	if err == nil {
		err = os.MkdirAll(a.localesDir(), 0o700)
	}
	if err == nil {
		tmp := filepath.Join(a.localesDir(), "."+id+".tmp")
		if err = os.WriteFile(tmp, b, 0o600); err == nil {
			err = os.Rename(tmp, filepath.Join(a.localesDir(), id+".json"))
		}
	}
	if err != nil {
		return "", errors.New(T("lang.err.copy", "reason", err.Error()))
	}
	return id, nil
}
