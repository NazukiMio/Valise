package enroll

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"valise/core/enroll"
	"valise/core/profile"
)

// DirItem is one empty directory inside a root.
type DirItem struct {
	Root     string
	Rel      string // slash-separated, relative to the root
	Category string
	Mode     fs.FileMode
}

// EmptyDirs lists the empty, classified directories of every existing root
// (never the root itself, nested instances or skipped directories). Body,
// cache and ignore directories are left out. The result is sorted by root
// and path.
func EmptyDirs(roots []enroll.Root, skip []string, c *profile.Classifier) []DirItem {
	for _, d := range append([]string{}, skip...) {
		if r := realPath(d); r != d {
			skip = append(skip, r)
		}
	}
	var out []DirItem
	for _, root := range roots {
		if !root.Exists {
			continue
		}
		var nested []string
		for _, other := range roots {
			if other.ID != root.ID && other.Exists && strings.HasPrefix(other.Dir(), root.Dir()+string(filepath.Separator)) {
				nested = append(nested, other.Dir())
			}
		}
		_ = filepath.WalkDir(root.Dir(), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if slices.Contains(nested, path) || slices.ContainsFunc(skip, func(n string) bool { return path == n || realPath(path) == n }) {
				return fs.SkipDir
			}
			if path == root.Dir() {
				return nil
			}
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) > 0 {
				return nil
			}
			rel, _ := filepath.Rel(root.Dir(), path)
			rel = filepath.ToSlash(rel)
			cat := c.Classify(root.ID, rel).Category
			switch cat {
			case "", profile.CategoryBody, profile.CategoryCache, profile.CategoryIgnore:
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			out = append(out, DirItem{Root: root.ID, Rel: rel, Category: cat, Mode: info.Mode()})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Root != out[j].Root {
			return out[i].Root < out[j].Root
		}
		return out[i].Rel < out[j].Rel
	})
	return out
}
