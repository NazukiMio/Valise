package actionjournal

import (
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"valise/core/action"
	"valise/core/profile"
	"valise/core/valise"
)

func DirectoryIdentity(p string) (action.Identity, error) {
	st, err := os.Lstat(p)
	if err != nil {
		return action.Identity{}, err
	}
	if !st.IsDir() {
		return action.Identity{}, valise.New("action_directory", "配置事务目录必须为真实目录", "检查活动目录，不能使用符号链接作为 root")
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return action.Identity{}, errors.New("directory identity unavailable")
	}
	return action.Identity{Device: uint64(sys.Dev), Inode: uint64(sys.Ino)}, nil
}

type FileFact struct {
	Mode fs.FileMode
	Size int64
	Hash [32]byte
	Link string
}

// TreeFacts hashes complete files for change detection; contents are never retained.
// Symlinks must be relative and stay within the root, including their full chain.
func TreeFacts(root string) (map[string]FileFact, int64, error) { return TreeFactsAllow(root, nil) }

// TreeFactsAllow also accepts the links a transaction plan handles
// (INSTALL.md 5.2.1): allow maps a slash path to the exact link text
// permitted there; such a link is recorded by its text, not followed.
func TreeFactsAllow(root string, allow map[string]string) (map[string]FileFact, int64, error) {
	if _, err := DirectoryIdentity(root); err != nil {
		return nil, 0, err
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, 0, err
	}
	facts := map[string]FileFact{}
	var total int64
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		f := FileFact{Mode: st.Mode(), Size: st.Size()}
		switch {
		case st.IsDir():
			f.Size = 0
		case st.Mode().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			h := sha256.New()
			n, err := io.Copy(h, in)
			ce := in.Close()
			if err != nil {
				return err
			}
			if ce != nil {
				return ce
			}
			if n != st.Size() {
				return errors.New("state changed during copy planning")
			}
			copy(f.Hash[:], h.Sum(nil))
			total += n
		case st.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if want, ok := allow[filepath.ToSlash(rel)]; ok && want == link {
				f.Link = link
				break
			}
			if filepath.IsAbs(link) {
				return errors.New("absolute state symlink cannot be staged")
			}
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			r, err := filepath.Rel(root, resolved)
			if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				return errors.New("state symlink leaves root")
			}
			f.Link = link
		default:
			return errors.New("state contains unsupported special file")
		}
		facts[filepath.ToSlash(rel)] = f
		return nil
	})
	return facts, total, err
}

// CopyTree writes only to a new private sibling. Failed copies remain for explicit cleanup.
func CopyTree(live, stage string, allow map[string]string, created ...func(action.Identity) error) (action.Identity, error) {
	before, _, err := TreeFactsAllow(live, allow)
	if err != nil {
		return action.Identity{}, err
	}
	if err = os.Mkdir(stage, 0700); err != nil {
		return action.Identity{}, err
	}
	id, err := DirectoryIdentity(stage)
	if err != nil {
		return action.Identity{}, err
	}
	for _, record := range created {
		if err := record(id); err != nil {
			return action.Identity{}, err
		}
	}
	// WalkDir visits parents before children; no source symlink is traversed.
	err = filepath.WalkDir(live, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(live, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(stage, rel)
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case st.IsDir():
			return os.Mkdir(dst, 0700)
		case st.Mode()&os.ModeSymlink != 0:
			return os.Symlink(before[filepath.ToSlash(rel)].Link, dst)
		case st.Mode().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, in)
			if err == nil {
				err = out.Chmod(st.Mode().Perm())
			}
			if err == nil {
				err = out.Sync()
			}
			ce := out.Close()
			if err == nil {
				err = ce
			}
			return err
		default:
			return errors.New("state file type changed")
		}
	})
	if err != nil {
		return action.Identity{}, err
	}
	// Restore directory permissions after populating descendants.
	err = filepath.WalkDir(stage, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(stage, p)
			if err = os.Chmod(p, before[filepath.ToSlash(rel)].Mode.Perm()); err != nil {
				return err
			}
			return syncDir(p)
		}
		return nil
	})
	if err != nil {
		return action.Identity{}, err
	}
	after, _, err := TreeFactsAllow(stage, allow)
	if err != nil {
		return action.Identity{}, err
	}
	sourceAfter, _, err := TreeFactsAllow(live, allow)
	if err != nil {
		return action.Identity{}, err
	}
	if !sameTree(before, after) || !sameTree(before, sourceAfter) {
		return action.Identity{}, errors.New("state copy changed; preserve staging and retry")
	}
	return DirectoryIdentity(stage)
}
func sameTree(a, b map[string]FileFact) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
func CheckWrites(root string, writes []string) error {
	for _, w := range writes {
		if !profile.SafeWritePath(w) {
			return errors.New("invalid declared write")
		}
		p := root
		for _, part := range strings.Split(w, "/") {
			p = filepath.Join(p, part)
			st, err := os.Lstat(p)
			if os.IsNotExist(err) {
				break
			}
			if err != nil {
				return err
			}
			if st.Mode()&os.ModeSymlink != 0 {
				return errors.New("declared write traverses symlink")
			}
		}
	}
	return nil
}
func CheckChanges(before, after map[string]FileFact, writes []string) error {
	allowed := func(k string) bool {
		for _, w := range writes {
			if k == w || strings.HasPrefix(k, w+"/") {
				return true
			}
		}
		return false
	}
	for k, v := range before {
		if next, ok := after[k]; (!ok || v != next) && !allowed(k) {
			return errors.New("configuration command changed undeclared path")
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok && !allowed(k) {
			return errors.New("configuration command created undeclared path")
		}
	}
	return nil
}
