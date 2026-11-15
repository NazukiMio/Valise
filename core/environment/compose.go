package environment

import (
	"errors"
	"path"
	"slices"
	"sort"
	"strings"

	"valise/core/identity"
	"valise/core/vault"
)

var ErrManifest = errors.New("environment: incompatible or unsafe data manifest")

type Material struct {
	Source Source
	Files  vault.Files
}

// DataEntry retains the exact snapshot from which its bytes are to be read.
// It describes data, not a new historical full snapshot. Programs always need
// their own verified private construction, even for full data targets.
type DataEntry struct {
	Root     string
	Path     string
	Category string
	Source   identity.SnapshotRef
	File     *vault.File
	Link     *vault.Symlink
}

type DataPlan struct {
	Entries             []DataEntry
	Removed             []DataEntry // baseline entries removed by whole-scope replacement
	NeedsDataFoundation bool        // missing data outside the selected scope; rebuild recorded defaults
}

// Compose replaces the ENTIRE selected tier scope. It never fills gaps from
// the live environment. classify must use the authenticated target profile and
// recorded overrides; LayoutDigest includes that classification contract.
// roots contains logical root IDs from that same verified layout, not paths.
func Compose(target Material, baseline *Material, roots []string, classify func(root, rel string) string) (DataPlan, error) {
	var out DataPlan
	if !target.Source.valid() || classify == nil || len(roots) == 0 || target.Files.Format != vault.FormatFiles {
		return out, ErrManifest
	}
	seenRoots := map[string]bool{}
	for _, root := range roots {
		if root == "" || seenRoots[root] || strings.ContainsAny(root, "/\\\x00\r\n") {
			return out, ErrManifest
		}
		seenRoots[root] = true
	}
	targetEntries, err := manifestEntries(target, roots, classify)
	if err != nil {
		return out, err
	}
	if baseline != nil {
		if !baseline.Source.valid() || baseline.Source.Tier != vault.TierFull || baseline.Source.Snapshot == target.Source.Snapshot || !compatible(target.Source, baseline.Source) || baseline.Source.Time.After(target.Source.Time) || target.Source.Tier == vault.TierFull || target.Source.Tier == vault.TierRaw {
			return out, ErrManifest
		}
		entries, err := manifestEntries(*baseline, roots, classify)
		if err != nil {
			return out, err
		}
		for _, e := range entries {
			if vault.InTier(target.Source.Tier, e.Category) {
				out.Removed = append(out.Removed, e)
			} else {
				out.Entries = append(out.Entries, e)
			}
		}
	} else {
		out.NeedsDataFoundation = target.Source.Tier != vault.TierFull && target.Source.Tier != vault.TierRaw
	}
	out.Entries = append(out.Entries, targetEntries...)
	if err := validateTree(out.Entries); err != nil {
		return DataPlan{}, err
	}
	sort.Slice(out.Entries, func(i, j int) bool { return entryKey(out.Entries[i]) < entryKey(out.Entries[j]) })
	return out, nil
}

func manifestEntries(m Material, roots []string, classify func(string, string) string) ([]DataEntry, error) {
	if m.Files.Format != vault.FormatFiles {
		return nil, ErrManifest
	}
	var entries []DataEntry
	add := func(root, rel string, f *vault.File, link *vault.Symlink) error {
		if !slices.Contains(roots, root) || !safeRel(rel) {
			return ErrManifest
		}
		category := classify(root, rel)
		if category == "" {
			category = vault.CategoryUnclassified
		}
		if !vault.InTier(m.Source.Tier, category) || f != nil && f.Category != category {
			return ErrManifest
		}
		entries = append(entries, DataEntry{root, rel, category, m.Source.Snapshot, f, link})
		return nil
	}
	for _, f := range m.Files.Files {
		copy := f
		copy.Blobs = slices.Clone(f.Blobs)
		if err := add(f.Root, f.Path, &copy, nil); err != nil {
			return nil, err
		}
	}
	for _, l := range m.Files.Symlinks {
		if l.Target == "" || strings.HasPrefix(l.Target, "/") || strings.ContainsAny(l.Target, "\\\x00\r\n") || !safeRel(path.Join(path.Dir(l.Path), l.Target)) {
			return nil, ErrManifest
		}
		copy := l
		if err := add(l.Root, l.Path, nil, &copy); err != nil {
			return nil, err
		}
	}
	return entries, validateTree(entries)
}

func safeRel(p string) bool {
	return p != "" && p != "." && p != ".." && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "../") && path.Clean(p) == p && !strings.ContainsAny(p, "\\\x00\r\n")
}

func entryKey(e DataEntry) string { return e.Root + "\x00" + e.Path }

func validateTree(entries []DataEntry) error {
	seen := map[string]bool{}
	for _, e := range entries {
		key := entryKey(e)
		if seen[key] {
			return ErrManifest
		}
		seen[key] = true
	}
	for _, e := range entries {
		for p := path.Dir(e.Path); p != "."; p = path.Dir(p) {
			if seen[e.Root+"\x00"+p] {
				return ErrManifest // a regular file or symlink cannot be a parent
			}
		}
	}
	return nil
}
