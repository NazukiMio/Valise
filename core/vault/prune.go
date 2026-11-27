package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// DeleteReport describes one DeleteSnapshots call.
type DeleteReport struct {
	// Deleted is the number of snapshots removed; Missing lists requested ids
	// that were already gone (deletion is idempotent).
	Deleted int      `json:"deleted"`
	Missing []string `json:"missing,omitempty"`
	// PacksRemoved counts orphan packs swept; FreedBytes is everything freed.
	PacksRemoved int   `json:"packs_removed"`
	FreedBytes   int64 `json:"freed_bytes"`
	// IndexesKept counts indexes kept because an older savepoint without a
	// full dependency table may still need their blob locations.
	IndexesKept int `json:"indexes_kept"`
}

// LeafDir is the directory of one leaf below a vault tree root. leaf is the
// period name ("savepoints" or <start>_<end>); "<instance>/<period>" is also
// accepted.
func LeafDir(vaultRoot, instance, leaf string) (string, error) {
	if inst, period, ok := strings.Cut(leaf, "/"); ok {
		if inst != instance {
			return "", errors.New("vault: leaf does not belong to the instance")
		}
		leaf = period
	}
	if _, _, ok := SplitLeaf(instance + "/" + leaf); !ok {
		return "", fmt.Errorf("vault: invalid leaf %s/%s", instance, leaf)
	}
	dir := filepath.Join(vaultRoot, instance, leaf)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("vault: leaf %s/%s does not exist", instance, leaf)
	}
	return dir, nil
}

func validSnapshotID(id string) bool { return strings.HasPrefix(id, "s_") && ValidID(id) }

func readMeta(dir, id string) (*Meta, error) {
	b, err := os.ReadFile(joinRel(dir, MetaPath(id)))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil || m.SnapshotID != id {
		return nil, errors.New("vault: unreadable snapshot meta " + id)
	}
	return &m, nil
}

// BundleOf returns the metas among ids that belong to a bundle backup
// (read-only; used to warn before deleting one member).
func BundleOf(vaultRoot, instance, leaf string, ids []string) ([]Meta, error) {
	dir, err := LeafDir(vaultRoot, instance, leaf)
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, id := range ids {
		if !validSnapshotID(id) {
			return nil, errors.New("vault: invalid snapshot id")
		}
		m, err := readMeta(dir, id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if m.BundleID != "" || len(m.BundleMembers) > 1 {
			out = append(out, *m)
		}
	}
	return out, nil
}

// DeletePlan lists what removing a set of snapshots takes out of a leaf.
type DeletePlan struct {
	// Remove is the ordered list of vault-relative files: snapshot meta and
	// header first, then indexes, then orphan packs.
	Remove []string
	// Missing lists requested ids without a meta file.
	Missing []string
	// IndexesKept counts target indexes retained for older savepoints.
	IndexesKept int
}

// Groups splits Remove into the ordered phases snapshots, indexes, packs.
func (p *DeletePlan) Groups() [][]string {
	var snaps, idx, packs []string
	for _, rel := range p.Remove {
		switch {
		case strings.HasPrefix(rel, SnapshotsDir+"/"):
			snaps = append(snaps, rel)
		case strings.HasPrefix(rel, IndexDir+"/"):
			idx = append(idx, rel)
		default:
			packs = append(packs, rel)
		}
	}
	var out [][]string
	for _, g := range [][]string{snaps, idx, packs} {
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// ListPacks lists a leaf's packs as vault-relative paths.
func ListPacks(dir string) ([]string, error) {
	packsDir := filepath.Join(dir, PacksDir)
	subs, err := os.ReadDir(packsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, sub := range subs {
		if !sub.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(packsDir, sub.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if id, err := packIDFromName(f.Name()); err == nil && f.Type().IsRegular() {
				out = append(out, PackPath(id))
			}
		}
	}
	return out, nil
}

// PlanDelete computes the deletion of ids from the leaf mirrored in dir. dir
// needs snapshots/*.meta.json and index/*.json (a remote copy may hold only
// those); packs lists every pack the leaf holds. Nothing is touched. A pack
// goes only when no remaining index references it, and an index of a deleted
// snapshot stays while an older savepoint without a full dependency table
// (not automatic, not promoted, same epoch, created at or after it) may need
// its blob locations. Packs and kept indexes are never rewritten.
func PlanDelete(dir string, packs, ids []string) (*DeletePlan, error) {
	if len(ids) == 0 {
		return nil, errors.New("vault: no snapshot selected")
	}
	del := map[string]bool{}
	for _, id := range ids {
		if !validSnapshotID(id) {
			return nil, errors.New("vault: invalid snapshot id " + id)
		}
		del[id] = true
	}
	if _, err := os.Stat(filepath.Join(dir, DescriptorFile)); err != nil {
		return nil, errors.New("vault: leaf does not exist")
	}
	metas, err := leafMetas(dir)
	if err != nil {
		return nil, err
	}
	plan := &DeletePlan{}
	order := make([]string, 0, len(del))
	for id := range del {
		order = append(order, id)
	}
	sort.Strings(order)
	keep := map[string]bool{}
	for _, id := range order {
		t, ok := metas[id]
		if !ok {
			plan.Missing = append(plan.Missing, id)
		}
		for rid, rem := range metas {
			if del[rid] || rem.Kind == KindAuto || rem.PromotedFrom != "" {
				continue
			}
			if ok && (rem.Epoch != t.Epoch || rem.CreatedAt.Before(t.CreatedAt)) {
				continue
			}
			if !indexFullDeps(dir, rid) {
				keep[id] = true
				break
			}
		}
	}
	for _, id := range order {
		plan.Remove = append(plan.Remove, MetaPath(id), SnapshotPath(id))
	}
	for _, id := range order {
		if keep[id] {
			plan.IndexesKept++
		} else {
			plan.Remove = append(plan.Remove, IndexPath(id))
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, IndexDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	used := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		id, isIdx := strings.CutSuffix(name, ".json")
		if strings.HasPrefix(name, ".") || !isIdx || (del[id] && !keep[id]) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, IndexDir, name))
		if err != nil {
			return nil, err
		}
		var ix Index
		if err := json.Unmarshal(b, &ix); err != nil {
			return nil, errors.New("vault: unreadable index " + name)
		}
		for _, be := range ix.Blobs {
			used[be.Pack] = true
		}
	}
	sort.Strings(packs)
	for _, rel := range packs {
		id, err := packIDFromName(path.Base(rel))
		if err != nil || rel != PackPath(id) || used[id] {
			continue
		}
		plan.Remove = append(plan.Remove, rel)
	}
	return plan, nil
}

// DeleteSnapshots removes the selected snapshots of one leaf and the packs
// no remaining index references. It needs no key. The caller serializes
// writers (the porter lock). Order is meta, header, index, packs: a crash
// leaves at worst orphans that the next call sweeps.
func DeleteSnapshots(vaultRoot, instance, leaf string, snapshotIDs []string) (DeleteReport, error) {
	var rep DeleteReport
	dir, err := LeafDir(vaultRoot, instance, leaf)
	if err != nil {
		return rep, err
	}
	if _, err := os.Lstat(filepath.Join(dir, DeletingFile)); err == nil {
		return rep, errors.New("vault: leaf is being deleted")
	}
	packs, err := ListPacks(dir)
	if err != nil {
		return rep, err
	}
	plan, err := PlanDelete(dir, packs, snapshotIDs)
	if err != nil {
		return rep, err
	}
	rep.Missing, rep.IndexesKept = plan.Missing, plan.IndexesKept
	removedID := map[string]bool{}
	for _, rel := range plan.Remove {
		n, ok, err := removeSized(joinRel(dir, rel))
		if err != nil {
			return rep, err
		}
		if !ok {
			continue
		}
		rep.FreedBytes += n
		switch {
		case strings.HasPrefix(rel, PacksDir+"/"):
			rep.PacksRemoved++
		default:
			base := path.Base(rel)
			removedID[strings.SplitN(base, ".", 2)[0]] = true
		}
	}
	for id := range removedID {
		if strings.HasPrefix(id, "s_") {
			rep.Deleted++
		}
	}
	return rep, nil
}

func leafMetas(dir string) (map[string]*Meta, error) {
	entries, err := os.ReadDir(filepath.Join(dir, SnapshotsDir))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]*Meta{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]*Meta{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".meta.json")
		if !ok || !validSnapshotID(id) {
			continue
		}
		m, err := readMeta(dir, id)
		if err != nil {
			return nil, err
		}
		out[id] = m
	}
	return out, nil
}

// indexFullDeps reports whether the snapshot's index is complete; an
// unreadable or missing index counts as incomplete (the safe answer).
func indexFullDeps(dir, id string) bool {
	b, err := os.ReadFile(joinRel(dir, IndexPath(id)))
	if err != nil {
		return false
	}
	var ix Index
	return json.Unmarshal(b, &ix) == nil && ix.FullDeps
}

func removeSized(p string) (int64, bool, error) {
	st, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if err := os.Remove(p); err != nil {
		return 0, false, err
	}
	return st.Size(), true, nil
}
