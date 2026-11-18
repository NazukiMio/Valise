package journey

import (
	"context"
	"os"
	"path"
	"sort"
	"strings"

	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vault"
)

// ReadRemote fetches only public descriptors, metadata and encrypted heads.
// The temporary selection is never a complete copy or a recovery cache.
func ReadRemote(ctx context.Context, remote stationsync.Remote, base, temp string, loc LocationRef) (Result, error) {
	entries, err := remote.List(ctx, base)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(temp, 0700); err != nil {
		return Result{}, err
	}
	dir, err := os.MkdirTemp(temp, "journey-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	var want []string
	for _, e := range entries {
		if e.Dir || !vault.ValidRel(e.Path) {
			continue
		}
		name := path.Base(e.Path)
		limit := int64(1 << 20)
		selected := name == vault.DescriptorFile || name == vault.DeletingFile || strings.HasSuffix(name, ".partial.json")
		if path.Base(path.Dir(e.Path)) == vault.SnapshotsDir {
			selected = strings.HasSuffix(name, ".meta.json") || strings.HasSuffix(name, ".age")
			if strings.HasSuffix(name, ".age") {
				limit = 16 << 20
			}
		}
		if !selected {
			continue
		}
		if e.Size < 0 || e.Size > limit {
			return Result{}, valise.New("invalid_metadata", "旅程元数据对象大小异常", "核对备份位置的内容后重试")
		}
		want = append(want, e.Path)
	}
	if len(want) > 0 {
		if err := remote.Fetch(ctx, base, want, dir); err != nil {
			return Result{}, err
		}
	}
	return Scan(ctx, []Source{{LocationRef: loc, Root: dir}})
}

// Merge preserves one point per stable reference and every observed location.
// Conflicting variants remain one explicitly conflicting point.
func Merge(results ...Result) Result {
	out := Result{}
	points := map[SnapshotRef]*TimelinePoint{}
	for _, result := range results {
		out.Partial = out.Partial || result.Partial
		out.Observations = append(out.Observations, result.Observations...)
		out.Gaps = append(out.Gaps, result.Gaps...)
		for _, item := range result.Points {
			p := points[item.SnapshotRef]
			if p == nil {
				p = &TimelinePoint{}
				*p = item
				p.Copies = append([]Copy(nil), item.Copies...)
				points[item.SnapshotRef] = p
				continue
			}
			for _, cp := range item.Copies {
				duplicate := false
				for _, old := range p.Copies {
					if old.HeaderDigest != cp.HeaderDigest || old.MetadataDigest != cp.MetadataDigest {
						p.MetadataState, out.Partial = "conflict", true
					}
					duplicate = duplicate || old.LocationRef == cp.LocationRef && old.SourceLocator == cp.SourceLocator && old.HeaderDigest == cp.HeaderDigest && old.MetadataDigest == cp.MetadataDigest
				}
				if !duplicate {
					p.Copies = append(p.Copies, cp)
				}
			}
		}
	}
	for _, p := range preferredPoints(points) {
		sort.Slice(p.Copies, func(i, j int) bool {
			a, b := p.Copies[i], p.Copies[j]
			return a.LocationRef.Kind+"/"+a.LocationRef.ID+"/"+a.LocationRef.AuthorityID+"/"+a.SourceLocator < b.LocationRef.Kind+"/"+b.LocationRef.ID+"/"+b.LocationRef.AuthorityID+"/"+b.SourceLocator
		})
		if p.MetadataState == "conflict" {
			for i := range p.Copies {
				p.Copies[i].MetadataState = "conflict"
			}
		}
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
	return out
}

// A promotion preserves the original encrypted header but is stored under a
// different vault ID. Prefer its physical reference only when that provenance
// matches, so restore and verification still open the advertised leaf.
func preferredPoints(points map[SnapshotRef]*TimelinePoint) []*TimelinePoint {
	hidden := make(map[SnapshotRef]bool)
	for _, p := range points {
		if p.Kind != vault.KindSavepoint || p.MetadataState == "conflict" || len(p.Copies) == 0 {
			continue
		}
		cp := p.Copies[0]
		vid, ok := vault.PromotionSource(cp.Metadata)
		if !ok || vid == p.SnapshotRef.VaultID {
			continue
		}
		ref := SnapshotRef{VaultID: vid, SnapshotID: p.SnapshotRef.SnapshotID}
		source := points[ref]
		if source != nil && source.Kind == vault.KindAuto && source.MetadataState != "conflict" &&
			source.RecordedInstanceID == p.RecordedInstanceID && len(source.Copies) != 0 &&
			cp.HeaderDigest != "" && cp.HeaderDigest == source.Copies[0].HeaderDigest {
			fresh := false
			for _, candidate := range p.Copies {
				fresh = fresh || candidate.Observation.Status == "fresh"
			}
			if fresh {
				hidden[ref] = true
			} else {
				// A cached or partially copied promotion must not hide the
				// currently readable automatic backup.
				hidden[p.SnapshotRef] = true
			}
		}
	}
	out := make([]*TimelinePoint, 0, len(points))
	for ref, point := range points {
		if !hidden[ref] {
			out = append(out, point)
		}
	}
	return out
}
