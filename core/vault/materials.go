package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Material lists for strict copies (docs/ARCHITECTURE.md 7.1.1). A
// material is one file of a leaf: slash path, size and, where it was read,
// its SHA-256. Stamp is the location's modification time as text: it only
// detects change, never stands in for a hash.
type Material struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	Stamp  string `json:"stamp,omitempty"`
}

// MaterialExcluded reports whether a leaf-relative path is outside the
// material list: the deletion marker and temporary upload names.
func MaterialExcluded(rel string) bool {
	base := path.Base(rel)
	return rel == DeletingFile || strings.HasPrefix(base, "tmp-") || strings.HasPrefix(base, ".tmp-") || strings.HasPrefix(base, ".part-")
}

// IsPackPath reports whether rel names a pack.
func IsPackPath(rel string) bool {
	return strings.HasPrefix(rel, PacksDir+"/") && strings.HasSuffix(rel, ".pack")
}

// PackIDOf is the pack id a pack path names, or "".
func PackIDOf(rel string) string {
	id, err := packIDFromName(path.Base(rel))
	if err != nil {
		return ""
	}
	return id
}

// Materials reads every file of the leaf at dir with its SHA-256.
func Materials(dir string) ([]Material, error) { return ListMaterials(dir, true) }

// ListMaterials lists the leaf at dir; packs are hashed only when packs is
// set (small files always are).
func ListMaterials(dir string, packs bool) ([]Material, error) {
	var out []Material
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		r, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(r)
		if MaterialExcluded(rel) {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("vault: %s is not a regular file", rel)
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if !packs && IsPackPath(rel) {
			out = append(out, Material{Path: rel, Size: st.Size(), Stamp: st.ModTime().UTC().Format(time.RFC3339Nano)})
			return nil
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		out = append(out, Material{Path: rel, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), Stamp: st.ModTime().UTC().Format(time.RFC3339Nano)})
		return nil
	})
	SortMaterials(out)
	return out, err
}

// SortMaterials orders a list by path.
func SortMaterials(list []Material) {
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
}

// MaterialsDigest identifies a complete list: paths, sizes and hashes in
// path order; modification times are left out.
func MaterialsDigest(list []Material) string {
	c := make([]Material, len(list))
	for i, m := range list {
		c[i] = Material{Path: m.Path, Size: m.Size, SHA256: m.SHA256}
	}
	SortMaterials(c)
	b, _ := json.Marshal(c)
	return SHA256Hex(b)
}

// MaterialDiff is how a location differs from the basis. Unhashed are
// small files the location gave no hash for; packs without a hash are
// left to the integrity check.
type MaterialDiff struct {
	Missing   []string `json:"missing,omitempty"`
	Extra     []string `json:"extra,omitempty"`
	Different []string `json:"different,omitempty"`
	Unhashed  []string `json:"unhashed,omitempty"`
}

// OK reports whether the location holds exactly the basis.
func (d MaterialDiff) OK() bool {
	return len(d.Missing)+len(d.Extra)+len(d.Different)+len(d.Unhashed) == 0
}

// CompareMaterials checks a location's list against the basis: every path
// present with the same size; small files must carry the same SHA-256, and
// a pack hash, when given, must equal the pack's name (the basis may list
// packs without hashes).
func CompareMaterials(basis, have []Material) MaterialDiff {
	var d MaterialDiff
	at := map[string]Material{}
	for _, m := range have {
		at[m.Path] = m
	}
	want := map[string]bool{}
	for _, b := range basis {
		want[b.Path] = true
		h, ok := at[b.Path]
		switch {
		case !ok:
			d.Missing = append(d.Missing, b.Path)
		case h.Size != b.Size:
			d.Different = append(d.Different, b.Path)
		case h.SHA256 == "" && !IsPackPath(b.Path):
			d.Unhashed = append(d.Unhashed, b.Path)
		case IsPackPath(b.Path) && h.SHA256 != "" && h.SHA256 != PackIDOf(b.Path):
			d.Different = append(d.Different, b.Path)
		case !IsPackPath(b.Path) && h.SHA256 != b.SHA256:
			d.Different = append(d.Different, b.Path)
		}
	}
	for _, h := range have {
		if !want[h.Path] {
			d.Extra = append(d.Extra, h.Path)
		}
	}
	sort.Strings(d.Missing)
	sort.Strings(d.Extra)
	sort.Strings(d.Different)
	sort.Strings(d.Unhashed)
	return d
}

// IntegrityCheck is the no-key check of docs/VAULT_FORMAT.md section 7 over
// a material list, for leaves that are not on a local disk. Every pack
// must carry its SHA-256; read returns the contents of small files (index
// and meta files). Problems name paths only.
func IntegrityCheck(list []Material, read func(rel string) ([]byte, error)) []Problem {
	var probs []Problem
	add := func(where, format string, a ...any) { probs = append(probs, Problem{where, fmt.Sprintf(format, a...)}) }
	have := map[string]Material{}
	packs := map[string]int64{}
	for _, m := range list {
		have[m.Path] = m
		if !IsPackPath(m.Path) {
			continue
		}
		id := PackIDOf(m.Path)
		switch {
		case id == "" || PackPath(id) != m.Path:
			add(m.Path, "文件名不符合格式")
		case m.SHA256 == "":
			add(m.Path, "内容未核对")
			packs[id] = m.Size // ranges can still be checked
		case m.SHA256 != id:
			add(m.Path, "SHA-256 与 pack_id 不一致（文件被篡改或损坏）")
		default:
			packs[id] = m.Size
		}
	}
	if _, ok := have["VAULT"]; !ok {
		add("VAULT", "缺少备份库描述文件")
	}
	for _, m := range list {
		switch {
		case strings.HasPrefix(m.Path, IndexDir+"/") && strings.HasSuffix(m.Path, ".json"):
			b, err := read(m.Path)
			var ix Index
			if err != nil || json.Unmarshal(b, &ix) != nil {
				add(m.Path, "无法读取或解析")
				continue
			}
			for _, e := range ix.Blobs {
				size, ok := packs[e.Pack]
				switch {
				case !ok:
					add("blob "+short(e.ID), "所在的 pack %s 不存在或未通过校验", short(e.Pack))
				case e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > size:
					add("blob "+short(e.ID), "offset/length 超出 pack 范围")
				}
			}
		case strings.HasPrefix(m.Path, SnapshotsDir+"/") && strings.HasSuffix(m.Path, ".meta.json"):
			b, err := read(m.Path)
			var meta Meta
			if err != nil || json.Unmarshal(b, &meta) != nil || MetaPath(meta.SnapshotID) != m.Path {
				add(m.Path, "无法读取或解析")
				continue
			}
			for _, need := range []struct{ rel, what string }{
				{SnapshotPath(meta.SnapshotID), "缺少加密的快照文件"},
				{IndexPath(meta.SnapshotID), "缺少 index 文件"},
				{EpochPath(meta.Epoch), fmt.Sprintf("epoch %d 没有对应的 epochs 文件", meta.Epoch)},
				{DedupeKeyPath(meta.Epoch), fmt.Sprintf("epoch %d 没有对应的去重参数文件", meta.Epoch)},
			} {
				if _, ok := have[need.rel]; !ok {
					add("snapshot "+meta.SnapshotID, "%s", need.what)
				}
			}
		}
	}
	return probs
}

// Snapshots are the snapshot ids a material list holds meta files for.
func Snapshots(list []Material) []string {
	var ids []string
	for _, m := range list {
		if strings.HasPrefix(m.Path, SnapshotsDir+"/") {
			if id, ok := strings.CutSuffix(path.Base(m.Path), ".meta.json"); ok {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids
}
