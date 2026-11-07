package journey

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"valise/core/vault"
)

// Scan rebuilds a metadata timeline. It never opens packs or encrypted file lists.
// Each location fails independently; cancellation returns no reusable query view.
func Scan(ctx context.Context, sources []Source) (Result, error) {
	out := Result{Points: []TimelinePoint{}, Observations: []Observation{}, Gaps: []Gap{}}
	points := map[SnapshotRef]*TimelinePoint{}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		now := time.Now().UTC()
		obs := Observation{LocationRef: source.LocationRef, Status: "fresh", ObservedAt: now, LastAttemptAt: now, Source: "local_filesystem"}
		if _, e := os.Lstat(source.Root + ".partial.json"); e == nil || !errors.Is(e, os.ErrNotExist) {
			obs.Status, obs.Error = "unknown", "partial_marker"
			out.Partial = true
			out.Gaps = append(out.Gaps, Gap{source.LocationRef, ".", "partial_marker"})
		}
		root, err := os.OpenRoot(source.Root)
		if err == nil {
			err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
				if e := ctx.Err(); e != nil {
					return e
				}
				if walkErr != nil {
					return walkErr
				}
				if d.Type()&os.ModeSymlink != 0 {
					return nil
				}
				if !d.IsDir() {
					return nil
				}
				if p != "." && strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				if strings.Count(p, "/") > 3 {
					return fs.SkipDir
				}
				_, e := root.Lstat(path.Join(p, vault.DescriptorFile))
				if errors.Is(e, os.ErrNotExist) {
					return nil
				}
				if e != nil {
					return e
				}
				scanLeaf(ctx, root, p, source.LocationRef, obs, points, &out)
				return fs.SkipDir
			})
			root.Close()
		}
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			obs.Status, obs.Error = "unknown", "location_unavailable"
			out.Partial = true
		}
		out.Observations = append(out.Observations, obs)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	for _, p := range points {
		out.Points = append(out.Points, *p)
	}
	sort.Slice(out.Points, func(i, j int) bool {
		a, b := out.Points[i], out.Points[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.After(b.CreatedAt)
		}
		if a.SnapshotRef.VaultID != b.SnapshotRef.VaultID {
			return a.SnapshotRef.VaultID < b.SnapshotRef.VaultID
		}
		return a.SnapshotRef.SnapshotID < b.SnapshotRef.SnapshotID
	})
	return out, nil
}

func readSmall(root *os.Root, p string, limit int64) ([]byte, error) {
	st, err := root.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid object")
	}
	f, err := root.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("object too large")
	}
	return b, err
}

func scanLeaf(ctx context.Context, root *os.Root, leaf string, loc LocationRef, obs Observation, points map[SnapshotRef]*TimelinePoint, out *Result) {
	gap := func(locator, code string) { out.Gaps = append(out.Gaps, Gap{loc, locator, code}); out.Partial = true }
	b, err := readSmall(root, path.Join(leaf, vault.DescriptorFile), 1<<20)
	var desc vault.Descriptor
	if err != nil || json.Unmarshal(b, &desc) != nil || desc.Format != vault.FormatVault || !vault.ValidID(desc.VaultID) || !strings.HasPrefix(desc.VaultID, "v_") {
		gap(leaf, "invalid_vault")
		return
	}
	blocked := false
	for _, marker := range []string{path.Join(leaf, vault.DeletingFile), leaf + ".partial.json"} {
		if _, e := root.Lstat(marker); e == nil {
			blocked = true
			gap(leaf, "partial_or_deleting")
		} else if !errors.Is(e, os.ErrNotExist) {
			blocked = true
			gap(leaf, "marker_unreadable")
		}
	}
	ents, err := fs.ReadDir(root.FS(), path.Join(leaf, vault.SnapshotsDir))
	if err != nil {
		gap(leaf, "snapshots_unreadable")
		return
	}
	metas := map[string]bool{}
	for _, e := range ents {
		if ctx.Err() != nil {
			return
		}
		id, ok := strings.CutSuffix(e.Name(), ".meta.json")
		if !ok || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		locator := path.Join(leaf, vault.MetaPath(id))
		if !vault.ValidID(id) || !strings.HasPrefix(id, "s_") {
			gap(locator, "invalid_metadata")
			continue
		}
		metas[id] = true
		b, err := readSmall(root, locator, 1<<20)
		var m vault.Meta
		if err != nil || json.Unmarshal(b, &m) != nil || !validMeta(m, id) {
			gap(locator, "invalid_metadata")
			continue
		}
		header, err := readSmall(root, path.Join(leaf, vault.SnapshotPath(id)), 16<<20)
		if err != nil || len(header) == 0 {
			gap(locator, "snapshot_header_missing")
			continue
		}
		canonical, _ := json.Marshal(m)
		copy := Copy{LocationRef: loc, SourceLocator: leaf, MetadataState: "metadata_only", HeaderDigest: vault.SHA256Hex(header), MetadataDigest: vault.SHA256Hex(canonical), Metadata: m, MaterialStatus: "unknown", Observation: obs}
		if blocked {
			copy.Observation.Status = "unknown"
			copy.Observation.Error = "partial_or_deleting"
		}
		ref := SnapshotRef{desc.VaultID, id}
		p := points[ref]
		if p == nil {
			p = &TimelinePoint{SnapshotRef: ref, CreatedAt: m.CreatedAt.UTC(), RecordedInstanceID: m.InstanceID, Scope: Scope{"preset", m.Tier}, MetadataState: "metadata_only", PlainBytes: m.PlainBytes, FileCount: m.FileCount, Copies: []Copy{}}
			p.Warnings = []string{"snapshot_time_uses_source_clock"}
			points[ref] = p
		} else if p.Copies[0].MetadataDigest != copy.MetadataDigest || p.Copies[0].HeaderDigest != copy.HeaderDigest {
			p.MetadataState = "conflict"
			out.Partial = true
			for i := range p.Copies {
				p.Copies[i].MetadataState = "conflict"
			}
		}
		if p.MetadataState == "conflict" {
			copy.MetadataState = "conflict"
		}
		p.Copies = append(p.Copies, copy)
	}
	for _, e := range ents {
		if id, ok := strings.CutSuffix(e.Name(), ".age"); ok && !strings.HasPrefix(e.Name(), ".") && !metas[id] {
			gap(path.Join(leaf, e.Name()), "metadata_missing")
		}
	}
}

func validMeta(m vault.Meta, id string) bool {
	_, tierOK := vault.TierCategories[m.Tier]
	return m.Format == vault.FormatMeta && m.SnapshotID == id && m.InstanceID != "" && !m.CreatedAt.IsZero() && m.Epoch > 0 && tierOK && m.FileCount >= 0 && m.PlainBytes >= 0 && m.AddedBytes >= 0
}
