package kit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
)

const (
	estimateMaxFiles = 5000
	estimateTimeout  = 2 * time.Second
)

var allowedLocalRootCategories = map[string]bool{
	profile.CategoryIdentity:  true,
	profile.CategoryMemory:    true,
	profile.CategoryWorkspace: true,
	profile.CategoryState:     true,
	profile.CategorySkills:    true,
	profile.CategoryConfig:    true,
	profile.CategorySecrets:   true,
}

// AllowedLocalRootCategory reports whether cat may be used as a default
// category for a user-attached local_root (never cache, ignore, or body).
func AllowedLocalRootCategory(cat string) bool {
	return allowedLocalRootCategories[cat]
}

var bannedExactDirs = []string{
	"/", "/etc", "/usr", "/var", "/bin", "/sbin",
	"/lib", "/lib64", "/boot", "/proc", "/sys", "/dev",
	"/root", "/tmp", "/run",
}

// bannedSubtreeDirs are system trees whose descendants are never valid user
// data directories (note /var and /tmp are banned exact-only so test temp dirs
// such as /var/folders/... or /tmp/... remain usable).
var bannedSubtreeDirs = []string{
	"/etc", "/usr", "/bin", "/sbin",
	"/lib", "/lib64", "/boot", "/proc", "/sys", "/dev",
	"/root", "/run",
}

func isPathSameOrUnder(p, base string) bool {
	if p == base {
		return true
	}
	if base == "/" {
		return strings.HasPrefix(p, "/")
	}
	return strings.HasPrefix(p, base+string(filepath.Separator))
}

func isBannedPath(home, p string) bool {
	clean := filepath.Clean(p)
	if home != "" {
		cleanHome := filepath.Clean(home)
		if clean == cleanHome {
			return true
		}
		if realHome, err := filepath.EvalSymlinks(cleanHome); err == nil && clean == realHome {
			return true
		}
	}
	for _, b := range bannedExactDirs {
		if clean == b {
			return true
		}
		if b != "/" {
			if rb, err := filepath.EvalSymlinks(b); err == nil && clean == rb {
				return true
			}
		}
	}
	for _, b := range bannedSubtreeDirs {
		if isPathSameOrUnder(clean, b) {
			return true
		}
		if rb, err := filepath.EvalSymlinks(b); err == nil && isPathSameOrUnder(clean, rb) {
			return true
		}
	}
	return false
}

func pathsOverlap(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	return isPathSameOrUnder(ca, cb) || isPathSameOrUnder(cb, ca)
}

func checkRootOverlap(roots map[string]string, target string) error {
	cleanTarget := filepath.Clean(target)
	realTarget, targetErr := filepath.EvalSymlinks(cleanTarget)
	for _, root := range roots {
		if root == "" {
			continue
		}
		cleanRoot := filepath.Clean(root)
		if isPathSameOrUnder(cleanTarget, cleanRoot) {
			return errors.New("路径位于实例 root 之内，请改用 classify 或 sqlite 提议")
		}
		if isPathSameOrUnder(cleanRoot, cleanTarget) {
			return errors.New("路径不能包含实例已有的 root 目录")
		}
		if targetErr == nil {
			if realRoot, err := filepath.EvalSymlinks(cleanRoot); err == nil {
				if isPathSameOrUnder(realTarget, realRoot) {
					return errors.New("路径位于实例 root 之内，请改用 classify 或 sqlite 提议")
				}
				if isPathSameOrUnder(realRoot, realTarget) {
					return errors.New("路径不能包含实例已有的 root 目录")
				}
			}
		}
	}
	return nil
}

// ValidateExternalRefPath checks that rawPath is a safe absolute path outside
// banned system/home roots and non-overlapping with existing instance roots.
func ValidateExternalRefPath(home, rawPath string, existingRoots map[string]string) (string, error) {
	raw := strings.TrimSpace(rawPath)
	if !filepath.IsAbs(raw) {
		return "", errors.New("路径必须是绝对路径")
	}
	clean := filepath.Clean(raw)
	if isBannedPath(home, clean) {
		return "", fmt.Errorf("不能选择根目录、家目录或系统目录：%s", clean)
	}
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		if isBannedPath(home, real) {
			return "", fmt.Errorf("路径经符号链接指向禁止目录：%s", real)
		}
	}
	if err := checkRootOverlap(existingRoots, clean); err != nil {
		return "", err
	}
	return clean, nil
}

// ValidateLocalRootDir strictly validates a directory before attaching it as a
// backed-up local_root: must be safe, exist on disk as a directory (or symlink
// resolving to a safe directory), and not overlap any existing root.
func ValidateLocalRootDir(home, rawPath string, existingRoots map[string]string) (string, error) {
	clean, err := ValidateExternalRefPath(home, rawPath, existingRoots)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("目录不存在或无法访问：%s", clean)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("附加路径必须是目录：%s", clean)
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("无法解析目录路径：%s", clean)
	}
	if isBannedPath(home, real) {
		return "", fmt.Errorf("路径经符号链接指向禁止目录：%s", real)
	}
	if err := checkRootOverlap(existingRoots, real); err != nil {
		return "", err
	}
	return clean, nil
}

// EstimateDir walks at most 5,000 regular files or 2 seconds (whichever comes
// first), skipping symlinks and non-regular entries, returning file and byte totals.
func EstimateDir(target string) (files int, bytes int64) {
	st, err := os.Stat(target)
	if err != nil {
		return 0, 0
	}
	if !st.IsDir() {
		if st.Mode().IsRegular() {
			return 1, st.Size()
		}
		return 0, 0
	}
	deadline := time.Now().Add(estimateTimeout)
	_ = filepath.WalkDir(target, func(_ string, d fs.DirEntry, err error) error {
		if files >= estimateMaxFiles || time.Now().After(deadline) {
			return fs.SkipAll
		}
		if err != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files++
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes
}

// DeriveRootID derives a unique, ValidRootID-compliant "ext-<slug>" ID for
// dirPath that does not collide with any key in existing.
func DeriveRootID(dirPath string, existing map[string]string) string {
	base := strings.ToLower(filepath.Base(filepath.Clean(dirPath)))
	var slug strings.Builder
	prevDash := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			slug.WriteRune(r)
			prevDash = false
		} else if !prevDash && slug.Len() > 0 {
			slug.WriteByte('-')
			prevDash = true
		}
	}
	s := strings.Trim(slug.String(), "-")
	if s == "" {
		s = "dir"
	}
	prefix := "ext-" + s
	if len(prefix) > 42 {
		prefix = strings.TrimRight(prefix[:42], "-")
	}
	if _, used := existing[prefix]; !used && enroll.ValidRootID(prefix) == nil {
		return prefix
	}
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s-%d", prefix, n)
		if _, used := existing[cand]; !used && enroll.ValidRootID(cand) == nil {
			return cand
		}
	}
}
