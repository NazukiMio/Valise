package install

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PathAdd puts dir on the login shell's PATH (docs/INSTALL.md section 3,
// path_add): one marked block appended to the shell's login file, never
// twice. It returns the file and the lines it holds.
func PathAdd(home, shell, dir, label string) (file string, lines []string, added bool, err error) {
	dir = strings.ReplaceAll(dir, "${HOME}", home)
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || !strings.HasPrefix(dir, home+"/") || strings.ContainsAny(dir, "$`\"'\\\n;&|<>*?") {
		return "", nil, false, errors.New("只能把家目录下的固定目录加入 PATH")
	}
	if !labelRe.MatchString(label) {
		return "", nil, false, errors.New("标签无效")
	}
	file = filepath.Join(home, ".profile")
	switch filepath.Base(shell) {
	case "zsh":
		file = filepath.Join(home, ".zprofile")
	case "bash":
		if _, err := os.Stat(filepath.Join(home, ".bash_profile")); err == nil {
			file = filepath.Join(home, ".bash_profile")
		}
	}
	marker := "# Added by Valise (" + label + "): put " + dir + " on PATH"
	lines = []string{marker, `case ":$PATH:" in *":` + dir + `:"*) ;; *) export PATH="` + dir + `:$PATH" ;; esac`}
	old, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", nil, false, err
	}
	for _, l := range strings.Split(string(old), "\n") {
		if l == marker {
			return file, lines, false, nil
		}
	}
	text := strings.Join(lines, "\n") + "\n"
	if len(old) > 0 && !strings.HasSuffix(string(old), "\n") {
		text = "\n" + text
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return "", nil, false, err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return "", nil, false, err
	}
	return file, lines, true, f.Close()
}

var labelRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
