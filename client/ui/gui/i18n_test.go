package gui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The desktop's non-test code writes no interface words: every sentence is
// a key in the catalog (docs/UI.md 7.6). Comments may say anything.
func TestNoInterfaceWordsInCode(t *testing.T) {
	han := regexp.MustCompile(`"[^"\n]*\p{Han}[^"\n]*"|` + "`[^`]*\\p{Han}[^`]*`")
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(b), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if m := han.FindString(code); m != "" {
				t.Errorf("%s:%d: words in code, use T(\"key\"): %s", f, i+1, m)
			}
		}
	}
}

// Every key the code names is in the built-in catalog, and every key in
// the catalog is named somewhere (a key built at run time is listed here).
func TestCatalogMatchesCode(t *testing.T) {
	built := []string{"service.short.", "leave.item.", "protect.period.", "lock.state."} // shortService joins the state's name
	used := map[string]bool{}
	keyLit := regexp.MustCompile(`"([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+)"`)
	files, _ := filepath.Glob("*.go")
	var all strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		all.Write(b)
		for _, m := range regexp.MustCompile(`\bT\("([^"]+)"`).FindAllStringSubmatch(string(b), -1) {
			used[m[1]] = true
			if _, ok := baseCatalog.Msgs[m[1]]; !ok && !strings.HasSuffix(m[1], ".") {
				t.Errorf("%s: key %q is not in i18n/zh-CN.json", f, m[1])
			}
		}
	}
	for _, m := range keyLit.FindAllStringSubmatch(all.String(), -1) {
		used[m[1]] = true
	}
	for key := range baseCatalog.Msgs {
		named := used[key]
		for _, p := range built {
			named = named || strings.HasPrefix(key, p)
		}
		if !named {
			t.Errorf("catalog key %q is not used", key)
		}
	}
}

func TestPlaceholdersAndPlural(t *testing.T) {
	if got := T("verdict.issues", "n", 3, "issues", 1); got != "3 个 Agent，1 件事需要你处理" {
		t.Fatalf("%q", got)
	}
	if got := T("no.such.key"); got != "no.such.key" {
		t.Fatalf("a missing key must show itself: %q", got)
	}
	c, err := parseCatalog([]byte(`{"format":"valise-locale/1","lang":"en","name":"English","messages":{
		"servers.agents":{"one":"{n} agent","other":"{n} agents"},"verdict.none":"No agents yet"}}`), baseCatalog)
	if err != nil {
		t.Fatal(err)
	}
	words = c
	defer func() { words = baseCatalog }()
	if T("servers.agents", "n", 1) != "1 agent" || T("servers.agents", "n", 2) != "2 agents" || T("verdict.none") != "No agents yet" {
		t.Fatalf("plural or text wrong: %q %q", T("servers.agents", "n", 1), T("servers.agents", "n", 2))
	}
	if T("nav.servers") != "服务器" {
		t.Fatal("a key the catalog lacks falls back to the built-in language")
	}
	if !c.Imported || c.Missing != len(baseCatalog.Msgs)-2 {
		t.Fatalf("imported %v missing %d", c.Imported, c.Missing)
	}
}

// A catalog with a mistake is not used, and says which entry is wrong.
func TestCatalogMistakes(t *testing.T) {
	head := `{"format":"valise-locale/1","lang":"en","name":"English","messages":`
	cases := map[string]string{
		head + `{"verdict.fine":"All fine"}}`:                                    "{n}",
		head + `{"made.up.key":"x"}}`:                                            "made.up.key",
		head + `{"Bad Key":"x"}}`:                                                "Bad Key",
		head + `{"verdict.none":"a\u0007b"}}`:                                    "控制字符",
		`{"format":"valise-locale/2","lang":"en","name":"x","messages":{}}`:      "format",
		`{"format":"valise-locale/1","lang":"English","name":"x","messages":{}}`: "lang",
		head + `{}, "script":"x"}`:                                               "格式不对",
	}
	for in, want := range cases {
		if _, err := parseCatalog([]byte(in), baseCatalog); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", in, err, want)
		}
	}
}

// An imported catalog is copied into the desktop folder, used, and marked
// as not reviewed; a broken one falls back to the built-in language.
func TestImportLocale(t *testing.T) {
	a, tt := world(t)
	src := filepath.Join(t.TempDir(), "en.json")
	os.WriteFile(src, []byte(`{"format":"valise-locale/1","lang":"en","name":"English","messages":{"verdict.issues":"{n} agents, {issues} to look at"}}`), 0o600)
	a.pickFile = func(string, string) (string, error) { return src, nil }
	a.importLocaleChosen()
	tt.Frame()
	defer func() { words = baseCatalog }()
	if a.prefs.Lang != "en" || !tt.HasText("2 agents, 2 to look at") || !tt.HasText("正在使用非官方翻译「English」：Valise 没有审核它的措辞 · "+
		T("lang.missing", "n", len(baseCatalog.Msgs)-1)) {
		t.Fatalf("not in use: %q %v %q", a.prefs.Lang, a.langErr, tt.Texts())
	}
	os.WriteFile(filepath.Join(a.localesDir(), "en.json"), []byte(`{"format":"valise-locale/1"}`), 0o600)
	a.loadLocales()
	a.chooseLocale()
	tt.Frame()
	if words != baseCatalog || !tt.HasText("2 个 Agent，2 件事需要你处理") || a.langErr == nil {
		t.Fatalf("no fallback: %v %q", a.langErr, tt.Texts())
	}
}
