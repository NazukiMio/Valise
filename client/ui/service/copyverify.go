package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Strict copies (docs/ARCHITECTURE.md 7.1.1): every place a period is kept
// is read again and compared with the server's material list; counts are
// never trusted.

// Copy states.
const (
	CopyComplete   = "complete"   // matches the basis and passed the no-key check
	CopyListed     = "listed"     // matches the basis; pack contents not checked yet
	CopyIncomplete = "incomplete" // differs from the basis or failed the check
	CopySelf       = "self"       // no basis: its own materials are consistent
	CopyUnknown    = "unknown"    // could not be read
	CopyUnchecked  = "unchecked"  // skipped by the user
	CopyDeleting   = "deleting"
	CopyAbsent     = "absent"
)

// Copy kinds.
const (
	KindServer  = "server"
	KindTarget  = "target"
	KindStation = "station"
	KindRepo    = "repo"
)

// CopyCheck is what one place holds of a period.
type CopyCheck struct {
	Where        string             `json:"where"` // "server", a target, station or repository name
	Kind         string             `json:"kind"`
	State        string             `json:"state"`
	Snapshots    int                `json:"snapshots"`
	Diff         vault.MaterialDiff `json:"diff"`
	Problems     []vault.Problem    `json:"problems,omitempty"`
	PendingBytes int64              `json:"pending_bytes,omitempty"`
	Fetched      int64              `json:"fetched_bytes,omitempty"`
	Reason       string             `json:"reason,omitempty"`
	CheckedAt    time.Time          `json:"checked_at"`
}

// CopyReport is the check of one period everywhere it is kept.
type CopyReport struct {
	Leaf    string `json:"leaf"`
	Current bool   `json:"current"`
	// Basis is the server's material list digest; empty when the server
	// could not give one (NoBasis says why).
	Basis          string      `json:"basis,omitempty"`
	BasisSnapshots int         `json:"basis_snapshots"`
	NoBasis        string      `json:"no_basis,omitempty"`
	Checks         []CopyCheck `json:"checks"`
}

// CompleteElsewhere reports whether a place other than except holds a
// complete copy.
func (r *CopyReport) CompleteElsewhere(except string) bool {
	for _, c := range r.Checks {
		if c.Where != except && c.State == CopyComplete {
			return true
		}
	}
	return false
}

// VerifyOptions control VerifyCopies.
type VerifyOptions struct {
	// Packs checks pack contents too (before deleting, or on request).
	Packs bool
	// Skip names places the user chose not to check.
	Skip map[string]bool
	// Confirm is asked before downloading pack contents from a place;
	// false leaves the place unchecked. Nil downloads without asking.
	Confirm func(where string, bytes int64) bool
	// Only, when set, limits the check to these places (the server's list
	// is always read as the basis).
	Only map[string]bool
}

func (o VerifyOptions) skipped(where string) bool { return o.Only != nil && !o.Only[where] }

// VerifyCopies checks one period of host everywhere it is kept.
func (s *Service) VerifyCopies(ctx context.Context, host, leaf string, o VerifyOptions) (*CopyReport, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	if _, _, ok := vault.SplitLeaf(leaf); !ok {
		return nil, valise.New("leaf_invalid", "无效的周期："+leaf, "使用 valise vault status 列出的名称")
	}
	rep := &CopyReport{Leaf: leaf}
	status, statusErr := s.VaultStatus(ctx, host)
	var basis *vaultrpc.LeafMaterialsResult
	server := CopyCheck{Where: "server", Kind: KindServer, CheckedAt: time.Now().UTC()}
	if statusErr == nil {
		var res vaultrpc.LeafMaterialsResult
		err := s.call(ctx, host, vaultrpc.MethodLeafMaterials, vaultrpc.LeafMaterialsParams{Leaf: leaf, Packs: o.Packs}, &res)
		switch ue, _ := valise.As(err); {
		case err == nil:
			basis = &res
		case ue != nil && ue.Code == "unsupported_capability":
			server.State, server.Reason = CopyUnknown, "这个 porter 版本不能核对，更新 porter 后再试"
		default:
			server.State, server.Reason = CopyUnknown, reasonOf(err)
		}
	} else {
		server.State, server.Reason = CopyUnknown, reasonOf(statusErr)
	}
	switch {
	case basis == nil:
		rep.NoBasis = server.Reason
	case !basis.Exists:
		server.State, rep.NoBasis = CopyAbsent, "服务器上已经没有这个周期"
	case basis.Deleting:
		server.State, rep.NoBasis = CopyDeleting, "服务器上这个周期的删除没有完成"
	default:
		rep.Basis, rep.Current = basis.Digest, basis.Current
		rep.BasisSnapshots = len(vault.Snapshots(basis.Materials))
		server.Snapshots, server.Problems, server.CheckedAt = rep.BasisSnapshots, basis.Integrity, basis.CheckedAt
		switch {
		case len(basis.Integrity) > 0:
			server.State = CopyIncomplete
		case !basis.Checked:
			server.State = CopyListed
		default:
			server.State = CopyComplete
		}
	}
	if !o.skipped("server") {
		rep.Checks = append(rep.Checks, server)
	}
	cache := s.loadVerification()
	var basisList []vault.Material
	if rep.Basis != "" {
		basisList = basis.Materials
	}
	serverName := ""
	seen := map[string]bool{}
	if statusErr == nil {
		serverName = status.ServerName
		for _, t := range status.Targets {
			seen[t.Name] = true
			if o.skipped(t.Name) {
				continue
			}
			c := CopyCheck{Where: t.Name, Kind: KindTarget}
			switch {
			case o.Skip[t.Name]:
				c.State, c.Reason = CopyUnchecked, "已跳过核对"
			case t.Kind == stationsync.MethodRclone && s.IsCloudHere(t.Name):
				// Logged in here too: read it from here, which also works
				// when the server cannot reach the drive.
				s.checkHere(ctx, host, serverName, leaf, &c, basisList, cache, o)
			case s.IsStation(t.Name):
				c.Kind = KindStation
				s.checkStation(ctx, host, serverName, leaf, &c, basisList, cache, o)
			default:
				s.checkTarget(ctx, host, leaf, &c, basisList, cache, o)
			}
			rep.Checks = append(rep.Checks, c)
		}
	}
	for _, cl := range s.Config.Clouds {
		if seen[cl.Name] || o.skipped(cl.Name) || statusErr != nil {
			continue
		}
		c := CopyCheck{Where: cl.Name, Kind: KindTarget}
		if o.Skip[cl.Name] {
			c.State, c.Reason = CopyUnchecked, "已跳过核对"
		} else {
			s.checkHere(ctx, host, serverName, leaf, &c, basisList, cache, o)
		}
		rep.Checks = append(rep.Checks, c)
	}
	for _, r := range s.Config.Repos {
		if o.skipped(r.Name) {
			continue
		}
		c := CopyCheck{Where: r.Name, Kind: KindRepo, CheckedAt: time.Now().UTC()}
		switch {
		case o.Skip[r.Name]:
			c.State, c.Reason = CopyUnchecked, "已跳过核对"
		case serverName == "":
			c.State, c.Reason = CopyUnknown, "不知道这台服务器在仓库里的文件夹名"
		default:
			checkRepo(filepath.Join(expandHome(r.Path), serverName, leafDirName(leaf)), &c, basisList, o.Packs)
		}
		rep.Checks = append(rep.Checks, c)
	}
	s.saveVerification(cache, host, rep)
	return rep, nil
}

func reasonOf(err error) string {
	if ue, ok := valise.As(err); ok {
		return ue.Reason
	}
	return err.Error()
}

// judge sets a place's state from what was read there.
func judge(c *CopyCheck, basis, have []vault.Material, problems []vault.Problem) {
	c.Snapshots = len(vault.Snapshots(have))
	unhashed := false
	for _, p := range problems {
		if p.What == "内容未核对" {
			unhashed = true
			continue
		}
		c.Problems = append(c.Problems, p)
	}
	if basis != nil {
		c.Diff = vault.CompareMaterials(basis, have)
	}
	switch {
	case !c.Diff.OK() || len(c.Problems) > 0:
		c.State = CopyIncomplete
	case unhashed:
		c.State = CopyListed
	case basis == nil:
		c.State = CopySelf
	default:
		c.State = CopyComplete
	}
}

// checkTarget reads a location the server reaches (porter target_verify).
func (s *Service) checkTarget(ctx context.Context, host, leaf string, c *CopyCheck, basis []vault.Material, cache *verificationState, o VerifyOptions) {
	q := vaultrpc.TargetVerifyParams{Target: c.Where, Leaf: leaf, Known: cache.known(host, c.Where, leaf)}
	var res vaultrpc.TargetVerifyResult
	err := s.call(ctx, host, vaultrpc.MethodTargetVerify, q, &res)
	if err == nil && o.Packs && res.PendingBytes > 0 {
		if o.Confirm == nil || o.Confirm(c.Where, res.PendingBytes) {
			q.Packs = true
			err = s.call(ctx, host, vaultrpc.MethodTargetVerify, q, &res)
		}
	}
	c.CheckedAt = time.Now().UTC()
	if err != nil {
		c.State, c.Reason = CopyUnknown, reasonOf(err)
		if ue, ok := valise.As(err); ok && ue.Code == "unsupported_capability" {
			c.Reason = "这个 porter 版本不能核对，更新 porter 后再试"
		}
		return
	}
	c.CheckedAt, c.PendingBytes, c.Fetched = res.CheckedAt, res.PendingBytes, res.FetchedBytes
	switch {
	case res.Deleting:
		c.State = CopyDeleting
	case !res.Exists || len(res.Materials) == 0:
		c.State = CopyAbsent
	default:
		judge(c, basis, res.Materials, res.Integrity)
		cache.remember(host, c.Where, leaf, res.Materials)
	}
}

// checkStation reads a Valise-managed station from this computer.
func (s *Service) checkStation(ctx context.Context, host, serverName, leaf string, c *CopyCheck, basis []vault.Material, cache *verificationState, o VerifyOptions) {
	c.CheckedAt = time.Now().UTC()
	st, err := s.station(c.Where)
	if err == nil && serverName == "" {
		err = valise.New("server_name_missing", "这台服务器还没有名字", "先给服务器起名")
	}
	if err == nil && leaf == vault.LegacyLeaf {
		err = valise.New("verify_legacy", "分档之前的旧备份库在这个位置上不能逐项核对", "删除前先把它取回到本机仓库再核对")
	}
	if err != nil {
		c.State, c.Reason = CopyUnknown, reasonOf(err)
		return
	}
	rs := s.relayStation(*st)
	s.checkRemote(ctx, host, leaf, c, rs, rs.Dest(strings.TrimRight(st.Path, "/")+"/"+serverName+"/"+leaf), basis, cache, o)
}

// checkHere reads a cloud drive logged in on this computer.
func (s *Service) checkHere(ctx context.Context, host, serverName, leaf string, c *CopyCheck, basis []vault.Material, cache *verificationState, o VerifyOptions) {
	c.CheckedAt = time.Now().UTC()
	rs, err := s.hereStation(ctx, c.Where)
	if err == nil && serverName == "" {
		err = valise.New("server_name_missing", "这台服务器还没有名字", "先给服务器起名")
	}
	if err == nil && leaf == vault.LegacyLeaf {
		err = valise.New("verify_legacy", "分档之前的旧备份库不复制到云盘", "")
	}
	if err != nil {
		c.State, c.Reason = CopyUnknown, reasonOf(err)
		return
	}
	s.checkRemote(ctx, host, leaf, c, rs, rs.Dest(cloudFolder+serverName+"/"+leaf), basis, cache, o)
}

// checkRemote reads a leaf at base through this computer's tools.
func (s *Service) checkRemote(ctx context.Context, host, leaf string, c *CopyCheck, rs stationsync.Station, base string, basis []vault.Material, cache *verificationState, o VerifyOptions) {
	var err error
	known := cache.lookup(host, c.Where, leaf)
	var reading *stationsync.LeafReading
	var deleting bool
	_, _, err = stationsync.With(ctx, localRunner{}, rs, func(r stationsync.Remote) error {
		entries, err := r.List(ctx, base)
		if err != nil {
			return err
		}
		var files []stationsync.Entry
		files, deleting = stationsync.LeafEntries(entries)
		if deleting || len(files) == 0 {
			return nil
		}
		packs := o.Packs
		if pending := stationsync.PendingPackBytes(files, known); packs && pending > 0 && o.Confirm != nil {
			packs = o.Confirm(c.Where, pending)
		}
		if err := os.MkdirAll(rs.Tmp, 0o700); err != nil {
			return err
		}
		reading, err = stationsync.ReadLeaf(ctx, r, base, files, rs.Tmp, packs, known)
		if err == nil && !packs {
			c.PendingBytes = stationsync.PendingPackBytes(files, known)
		}
		return err
	})
	c.CheckedAt = time.Now().UTC()
	switch {
	case err != nil:
		c.State, c.Reason = CopyUnknown, reasonOf(err)
	case deleting:
		c.State = CopyDeleting
	case reading == nil:
		c.State = CopyAbsent
	default:
		c.Fetched = reading.Fetched
		read := func(rel string) ([]byte, error) {
			if b, ok := reading.Small[rel]; ok {
				return b, nil
			}
			return nil, os.ErrNotExist
		}
		judge(c, basis, reading.Materials, vault.IntegrityCheck(reading.Materials, read))
		cache.remember(host, c.Where, leaf, reading.Materials)
	}
}

// checkRepo reads a repository folder on this computer.
func checkRepo(dir string, c *CopyCheck, basis []vault.Material, packs bool) {
	if _, err := os.Stat(dir + ".partial.json"); err == nil {
		c.State, c.Reason = CopyIncomplete, "只取回了部分备份，不是完整周期"
		return
	}
	if _, err := os.Stat(filepath.Join(dir, vault.DeletingFile)); err == nil {
		c.State = CopyDeleting
		return
	}
	st, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		c.State = CopyAbsent
		return
	case err != nil:
		c.State, c.Reason = CopyUnknown, err.Error()
		return
	case !st.IsDir():
		c.State, c.Reason = CopyUnknown, "不是文件夹"
		return
	}
	have, err := vault.ListMaterials(dir, packs)
	c.CheckedAt = time.Now().UTC()
	if err != nil {
		c.State, c.Reason = CopyUnknown, err.Error()
		return
	}
	read := func(rel string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))) }
	judge(c, basis, have, vault.IntegrityCheck(have, read))
}

// CheckAfterCopy compares the periods just copied to a place with the
// server's lists (period_material, no pack downloads; docs/ARCHITECTURE.md
// 7.1.1). A failed check is a result, never an error of the copy.
func (s *Service) CheckAfterCopy(ctx context.Context, host, where string, leaves []string) []CopyCheck {
	var out []CopyCheck
	for _, leaf := range leaves {
		if leaf == vault.LegacyLeaf {
			continue
		}
		rep, err := s.VerifyCopies(ctx, host, leaf, VerifyOptions{Only: map[string]bool{where: true}})
		if err != nil {
			continue
		}
		for _, c := range rep.Checks {
			if c.Where == where {
				if c.Reason != "" {
					c.Reason = leaf + "：" + c.Reason
				}
				out = append(out, c)
			}
		}
	}
	return out
}
