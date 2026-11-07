package seal

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// Strict copies (docs/ARCHITECTURE.md 7.1.1): the server leaf's material
// list is the basis; a location is read and hashed, never trusted by count.

// LeafMaterials lists and checks one leaf of the server's vault.
func LeafMaterials(p Paths, leaf string, packs bool) (*vaultrpc.LeafMaterialsResult, error) {
	dir, err := p.LeafDir(leaf)
	if err != nil {
		return nil, err
	}
	res := &vaultrpc.LeafMaterialsResult{Leaf: leaf, Materials: []vault.Material{}, Integrity: []vault.Problem{}, CheckedAt: time.Now().UTC()}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return res, nil
	}
	res.Exists = true
	res.Current = leaf != vault.LegacyLeaf && refuseCurrent(p, leaf) != nil
	if _, err := os.Stat(filepath.Join(dir, vault.DeletingFile)); err == nil {
		res.Deleting = true
	}
	if res.Materials, err = vault.ListMaterials(dir, packs); err != nil {
		return nil, err
	}
	res.Digest = vault.MaterialsDigest(res.Materials)
	if !packs {
		return res, nil
	}
	res.Checked = true
	if probs := vault.IntegrityCheck(res.Materials, localReader(dir)); probs != nil {
		res.Integrity = probs
	}
	return res, nil
}

func localReader(dir string) func(string) ([]byte, error) {
	return func(rel string) ([]byte, error) {
		if !vault.ValidRel(rel) {
			return nil, errors.New("invalid path")
		}
		return os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	}
}

// leafBase is where a leaf lives on a target.
func leafBase(p Paths, t *vaultrpc.Target, leaf string) (string, error) {
	tree, legacy, err := remoteBases(p, t)
	if err != nil {
		return "", err
	}
	if leaf == vault.LegacyLeaf {
		if legacy == "" {
			return "", valise.New("leaf_missing", "无法确定旧金库在 "+t.Name+" 上的位置", "服务器上的旧金库已经不存在")
		}
		return legacy, nil
	}
	if tree == "" {
		return "", valise.New("server_name_missing", "这台服务器还没有名字", "先给服务器起名：valise server name <服务器> <名字>")
	}
	return tree + "/" + leaf, nil
}

// TargetVerify reads one leaf on a location the server reaches. An rsync
// location is compared by checksum with the server's own leaf (no
// download); otherwise small files, and packs when asked, are downloaded
// into the private tmp directory, hashed and deleted.
func TargetVerify(ctx context.Context, e *porterenroll.Env, p Paths, q vaultrpc.TargetVerifyParams) (*vaultrpc.TargetVerifyResult, error) {
	if _, _, ok := vault.SplitLeaf(q.Leaf); !ok {
		return nil, valise.New("leaf_invalid", "无效的周期："+q.Leaf, "使用备份位置列表中的名称")
	}
	t, err := findTarget(p, q.Target)
	if err != nil {
		return nil, err
	}
	if t.Station != nil {
		return nil, valise.New("verify_from_client", t.Name+" 由 Valise 管理，副本由这台电脑核对", "在 Valise 里核对这个备份位置")
	}
	base, err := leafBase(p, t, q.Leaf)
	if err != nil {
		return nil, err
	}
	local, err := p.LeafDir(q.Leaf)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.Tmp(), 0o700); err != nil {
		return nil, err
	}
	known := knownPacks(q.Known)
	res := &vaultrpc.TargetVerifyResult{Target: t.Name, Leaf: q.Leaf, Materials: []vault.Material{}, Integrity: []vault.Problem{}}
	res.Method, _, err = withRemote(ctx, e, p, t, func(r stationsync.Remote) error {
		entries, err := r.List(ctx, base)
		if err != nil {
			return err
		}
		files, deleting := stationsync.LeafEntries(entries)
		res.Exists, res.Deleting = len(entries) > 0, deleting
		if len(files) == 0 {
			return nil
		}
		var small map[string][]byte
		res.Materials, small, err = readLocation(ctx, r, base, local, files, p.Tmp(), q.Packs, known, res)
		if err != nil {
			return err
		}
		read := func(rel string) ([]byte, error) {
			if b, ok := small[rel]; ok {
				return b, nil
			}
			return nil, errors.New("not read")
		}
		if probs := vault.IntegrityCheck(res.Materials, read); probs != nil {
			res.Integrity = probs
		}
		return nil
	})
	res.CheckedAt = time.Now().UTC()
	if err != nil {
		return nil, err
	}
	return res, nil
}

// readLocation establishes the hashes of a location's files.
func readLocation(ctx context.Context, r stationsync.Remote, base, local string, files []stationsync.Entry, tmp string, packs bool, known stationsync.KnownPack, res *vaultrpc.TargetVerifyResult) ([]vault.Material, map[string][]byte, error) {
	small := map[string][]byte{}
	cmp, ok := r.(stationsync.ChecksumComparer)
	if _, err := os.Stat(local); !ok || err != nil {
		reading, err := stationsync.ReadLeaf(ctx, r, base, files, tmp, packs, known)
		if err != nil {
			return nil, nil, err
		}
		res.Hashed, res.FetchedBytes = reading.Hashed, reading.Fetched
		if !packs {
			res.PendingBytes = stationsync.PendingPackBytes(files, known)
		}
		return reading.Materials, reading.Small, nil
	}
	differ, err := cmp.ChecksumDiff(ctx, local, base)
	if err != nil {
		return nil, nil, err
	}
	odd := map[string]bool{}
	for _, d := range differ {
		odd[d] = true
	}
	mine, err := vault.Materials(local)
	if err != nil {
		return nil, nil, err
	}
	same := map[string]vault.Material{}
	for _, m := range mine {
		if !odd[m.Path] {
			same[m.Path] = m
		}
	}
	var out []vault.Material
	var rest []stationsync.Entry
	for _, f := range files {
		if m, ok := same[f.Path]; ok && m.Size == f.Size {
			out = append(out, vault.Material{Path: f.Path, Size: f.Size, SHA256: m.SHA256, Stamp: f.Stamp})
			if !vault.IsPackPath(f.Path) {
				b, err := os.ReadFile(filepath.Join(local, filepath.FromSlash(f.Path)))
				if err != nil {
					return nil, nil, err
				}
				small[f.Path] = b
			}
			continue
		}
		rest = append(rest, f)
	}
	if len(rest) > 0 {
		reading, err := stationsync.ReadLeaf(ctx, r, base, rest, tmp, packs, known)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, reading.Materials...)
		for k, v := range reading.Small {
			small[k] = v
		}
		res.Hashed, res.FetchedBytes = reading.Hashed, reading.Fetched
		if !packs {
			res.PendingBytes = stationsync.PendingPackBytes(rest, known)
		}
	}
	vault.SortMaterials(out)
	return out, small, nil
}

func knownPacks(list []vault.Material) stationsync.KnownPack {
	at := map[string]vault.Material{}
	for _, m := range list {
		if vault.IsPackPath(m.Path) && m.SHA256 != "" && m.Stamp != "" && path.Clean(m.Path) == m.Path && !strings.Contains(m.Path, "..") {
			at[m.Path] = m
		}
	}
	return func(rel string, size int64, stamp string) (string, bool) {
		m, ok := at[rel]
		if !ok || m.Size != size || m.Stamp != stamp {
			return "", false
		}
		return m.SHA256, true
	}
}
