package actionjournal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"valise/core/action"
	"valise/core/valise"
)

// Staged link handling for configuration transactions (docs/INSTALL.md
// 5.2.1). Links are classified by their fully resolved target with
// generic rules only: no harness name or link name appears here.

func linkError(reason string) error {
	return valise.New("action_link", reason, "检查状态目录里的这个链接；Valise 只处理指向状态目录内部或已登记程序目录的链接")
}

// inside reports whether path is dir or below it (both cleaned, real).
func inside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ScanLinks classifies the symlinks under the state root. A relative link
// whose resolution stays inside the root needs no handling and is not
// listed. It returns the handled links and the size of the program copies
// body links need.
func ScanLinks(root string, bodies []string) ([]action.Link, int64, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, 0, err
	}
	var realBodies []string
	for _, b := range bodies {
		if r, err := filepath.EvalSymlinks(b); err == nil {
			realBodies = append(realBodies, r)
		}
	}
	var links []action.Link
	var bytes int64
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		text, err := os.Readlink(p)
		if err != nil {
			return err
		}
		target, err := filepath.EvalSymlinks(p)
		if err != nil {
			return linkError("状态目录里的链接 " + rel + " 悬空或循环（指向 " + text + "）")
		}
		st, err := os.Stat(target)
		if err != nil {
			return err
		}
		if !st.IsDir() && !st.Mode().IsRegular() {
			return linkError("状态目录里的链接 " + rel + " 指向的不是普通目录或文件")
		}
		if inside(target, root) {
			if !filepath.IsAbs(text) {
				return nil // a relative link inside the root: copied as is
			}
			links = append(links, action.Link{Rel: rel, Text: text, Target: target, Class: action.LinkInside, Dir: st.IsDir()})
			return nil
		}
		for _, b := range realBodies {
			if inside(target, b) {
				digest, size, err := PathDigest(target)
				if err != nil {
					return err
				}
				bytes += size
				links = append(links, action.Link{Rel: rel, Text: text, Target: target, Class: action.LinkBody, Dir: st.IsDir(), Digest: digest})
				return nil
			}
		}
		if len(realBodies) == 0 {
			return linkError("状态目录里的链接 " + rel + " 指向状态目录之外（" + target + "），而这个安装没有记录程序目录，来源无法核实")
		}
		return linkError("状态目录里的链接 " + rel + " 指向状态目录与已登记程序目录之外：" + target)
	})
	return links, bytes, err
}

// OriginalTexts allows each handled link with its original text.
func OriginalTexts(links []action.Link) map[string]string {
	out := map[string]string{}
	for _, l := range links {
		out[l.Rel] = l.Text
	}
	return out
}

// pathFacts lists a program tree (or one file) for change detection; any
// link is recorded by its text and never followed.
func pathFacts(path string) (map[string]FileFact, int64, error) {
	facts := map[string]FileFact{}
	var total int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		f := FileFact{Mode: st.Mode()}
		switch {
		case st.IsDir():
		case st.Mode().IsRegular():
			h, n, err := hashFile(p)
			if err != nil {
				return err
			}
			f.Size, f.Hash, total = n, h, total+n
		case st.Mode()&os.ModeSymlink != 0:
			if f.Link, err = os.Readlink(p); err != nil {
				return err
			}
		default:
			return errors.New("program copy contains unsupported special file")
		}
		facts[filepath.ToSlash(rel)] = f
		return nil
	})
	return facts, total, err
}

func hashFile(p string) ([32]byte, int64, error) {
	var sum [32]byte
	in, err := os.Open(p)
	if err != nil {
		return sum, 0, err
	}
	defer in.Close()
	h := sha256.New()
	n, err := io.Copy(h, in)
	copy(sum[:], h.Sum(nil))
	return sum, n, err
}

func factsDigest(facts map[string]FileFact) string {
	b, _ := json.Marshal(facts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// PathDigest is the digest and size of a program tree or file.
func PathDigest(path string) (string, int64, error) {
	facts, size, err := pathFacts(path)
	if err != nil {
		return "", 0, err
	}
	return factsDigest(facts), size, nil
}

// copyPath copies a program tree or file into a new private path; links
// are copied by text.
func copyPath(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case st.IsDir():
			return os.Mkdir(out, 0o700)
		case st.Mode()&os.ModeSymlink != 0:
			t, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(t, out)
		case st.Mode().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			w, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, in)
			if err == nil {
				err = w.Sync()
			}
			if ce := w.Close(); err == nil {
				err = ce
			}
			return err
		}
		return errors.New("program copy contains unsupported special file")
	})
}

// fixModes gives the copy the source's permission bits after populating it.
func fixModes(src, dst string) error {
	facts, _, err := pathFacts(src)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(facts))
	for k := range facts {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys))) // children before parents
	for _, k := range keys {
		f := facts[k]
		if f.Mode&os.ModeSymlink != 0 {
			continue
		}
		if err := os.Chmod(filepath.Join(dst, filepath.FromSlash(k)), f.Mode.Perm()); err != nil {
			return err
		}
	}
	return nil
}

// CopyDir is where a body link's private program copy lives.
func CopyDir(private string, i int) string {
	return filepath.Join(private, "program", strconv.Itoa(i))
}

func swapLink(p, from, to string) error {
	cur, err := os.Readlink(p)
	if err != nil || cur != from {
		return errors.New("staged link changed: " + p)
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	if err := os.Symlink(to, p); err != nil {
		return err
	}
	if cur, err = os.Readlink(p); err != nil || cur != to {
		return errors.New("staged link could not be set: " + p)
	}
	return nil
}

// StageLinks points every handled link in stage at its staged counterpart:
// an inside link at the same place under stage, a body link at a private
// copy of its target. It returns the staged texts.
func StageLinks(stage, live, private string, links []action.Link) (map[string]string, error) {
	staged := map[string]string{}
	for i, l := range links {
		var to string
		switch l.Class {
		case action.LinkInside:
			rel, err := filepath.Rel(live, l.Target)
			if err != nil || !inside(l.Target, live) {
				return nil, linkError("链接 " + l.Rel + " 的目标已不在状态目录内")
			}
			to = filepath.Join(stage, rel)
		case action.LinkBody:
			to = CopyDir(private, i)
			if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
				return nil, err
			}
			if err := copyPath(l.Target, to); err != nil {
				return nil, err
			}
			if err := fixModes(l.Target, to); err != nil {
				return nil, err
			}
			if d, _, err := PathDigest(to); err != nil || d != l.Digest {
				return nil, linkError("链接 " + l.Rel + " 指向的程序代码在复制时与计划不一致")
			}
		default:
			return nil, linkError("未知的链接处理方式：" + l.Class)
		}
		if err := swapLink(filepath.Join(stage, filepath.FromSlash(l.Rel)), l.Text, to); err != nil {
			return nil, err
		}
		staged[l.Rel] = to
	}
	return staged, nil
}

// CheckPrograms verifies after the steps that neither a private program
// copy nor its original changed. A change in the copy names the file.
func CheckPrograms(private string, links []action.Link) error {
	for i, l := range links {
		if l.Class != action.LinkBody {
			continue
		}
		cp := CopyDir(private, i)
		facts, _, err := pathFacts(cp)
		if err != nil {
			return err
		}
		if factsDigest(facts) != l.Digest {
			orig, _, err := pathFacts(l.Target)
			name := l.Rel
			if err == nil {
				name += "/" + firstDifference(orig, facts)
			}
			return linkError("配置命令改动了程序代码的私有副本：" + name + "；需要写那里时须先在 profile 中声明")
		}
		if d, _, err := PathDigest(l.Target); err != nil || d != l.Digest {
			return linkError("链接 " + l.Rel + " 指向的原程序代码在执行期间发生变化")
		}
	}
	return nil
}

func firstDifference(a, b map[string]FileFact) string {
	var keys []string
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			keys = append(keys, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "?"
	}
	return keys[0]
}

// InAreas reports whether the slash path rel is a declared write area or
// lies below one.
func InAreas(rel string, areas []string) bool {
	for _, w := range areas {
		if rel == w || strings.HasPrefix(rel, w+"/") {
			return true
		}
	}
	return false
}

// RestoreLinks puts every original link text back in stage, checking that
// each link still holds its staged text. Inside a declared write area a
// link the command removed or rewrote is left as the command left it
// (INSTALL.md 5.2.1); WriteAreaLinks has checked any new text.
func RestoreLinks(stage string, links []action.Link, staged map[string]string, areas []string) error {
	for _, l := range links {
		p := filepath.Join(stage, filepath.FromSlash(l.Rel))
		if InAreas(l.Rel, areas) {
			if cur, err := os.Readlink(p); err != nil || cur != staged[l.Rel] {
				continue
			}
		}
		if err := swapLink(p, staged[l.Rel], l.Text); err != nil {
			return err
		}
	}
	return nil
}

// WriteAreaLinks checks the links the steps created or rewrote inside the
// declared write areas of stage (INSTALL.md 5.2.1). An absolute text must
// lead into the live state root (taken over by the stage's counterpart,
// which must exist) or into a recorded program location; one leading into
// the private area, dangling or anywhere else is refused. Relative texts
// are left to TreeFacts. It returns the accepted texts by slash path.
func WriteAreaLinks(stage, live, private string, areas []string, staged map[string]string, bodies []string) (map[string]string, error) {
	var realBodies []string
	for _, b := range bodies {
		if r, err := filepath.EvalSymlinks(b); err == nil {
			realBodies = append(realBodies, r)
		}
	}
	realStage, err := filepath.EvalSymlinks(stage)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, area := range areas {
		top := filepath.Join(stage, filepath.FromSlash(area))
		if _, err := os.Lstat(top); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink == 0 {
				return nil
			}
			r, err := filepath.Rel(stage, p)
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(r)
			text, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if want, ok := staged[rel]; (ok && want == text) || !filepath.IsAbs(text) {
				return nil
			}
			clean := filepath.Clean(text)
			got, gotErr := filepath.EvalSymlinks(clean)
			if inside(clean, private) || (gotErr == nil && inside(got, private)) {
				return linkError("配置命令新建的链接 " + rel + " 指向本次暂存（" + text + "），交换后会失效")
			}
			var lr string
			switch {
			case inside(clean, live):
				lr, _ = filepath.Rel(live, clean)
			case gotErr == nil && inside(got, live):
				lr, _ = filepath.Rel(live, got)
			}
			if lr != "" {
				at, err := filepath.EvalSymlinks(filepath.Join(stage, lr))
				if err != nil || !inside(at, realStage) {
					return linkError("配置命令新建的链接 " + rel + " 指向状态目录内不存在的位置：" + text)
				}
				out[rel] = text
				return nil
			}
			if gotErr != nil {
				return linkError("配置命令新建的链接 " + rel + " 悬空或循环（指向 " + text + "）")
			}
			ok := false
			for _, b := range realBodies {
				ok = ok || inside(got, b)
			}
			st, err := os.Stat(got)
			if !ok || err != nil || (!st.IsDir() && !st.Mode().IsRegular()) {
				return linkError("配置命令新建的链接 " + rel + " 指向状态目录与已登记程序目录之外：" + text)
			}
			out[rel] = text
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Merged is a with b's entries added, b winning.
func Merged(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
