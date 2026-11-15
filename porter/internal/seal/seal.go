package seal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/porter/internal/activeview"
	porterenroll "valise/porter/internal/enroll"
)

const gitTimeout = 60 * time.Second

// Seal seals one instance into the server vault. It only reads the
// instance's files; all writes go to the vault and the temporary directory.
func Seal(ctx context.Context, e *porterenroll.Env, p Paths, params vaultrpc.SealParams, initiator string) (*vaultrpc.SealReport, error) {
	rep, err := sealLocked(ctx, e, p, params, initiator)
	if err == nil {
		rep.AutoSync = startAutoSync(p)
	}
	return rep, err
}

// sealLocked does the seal under the vault lock; Seal starts the automatic
// cloud backup after the lock is released.
func sealLocked(ctx context.Context, e *porterenroll.Env, p Paths, params vaultrpc.SealParams, initiator string) (rep *vaultrpc.SealReport, err error) {
	start := time.Now()
	if _, ok := vault.TierCategories[params.Tier]; !ok {
		return nil, valise.New("tier_invalid", "未知的封存档位："+params.Tier, "可选 soul、soul-secrets、full")
	}
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	active, err := activeBackup(ctx, p.Home, params.InstanceID)
	if err != nil {
		return nil, err
	}
	var rec *enroll.Record
	var prof *profile.Profile
	if active != nil {
		rec = active.Record
		selected := active.Profile.ForAgent(rec.Name)
		rec.ApplyEntryPorts(&selected)
		prof = &selected
	} else {
		rec, err = porterenroll.ReadRecord(p.Home, params.InstanceID)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			prof, err = porterenroll.ReadProfile(p.Home, params.InstanceID)
			if err != nil {
				return nil, err
			}
		}
	}
	if rec == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+params.InstanceID, "先运行 valise enroll <服务器> --accept")
	}
	if rec.Superseded != nil {
		return nil, valise.New("instance_superseded", "实例 "+params.InstanceID+" 的目录已被实例 "+rec.Superseded.By+" 取代（操作 "+rec.Superseded.OpID+"）",
			"封存 "+rec.Superseded.By+"；不再需要旧实例时用 valise old cleanup 清理")
	}
	if prof.ProfileVersion != rec.ProfileVersion || prof.ID != rec.ProfileID {
		return nil, valise.New("instance_profile_mismatch", "实例记录与接入时的 profile 不一致", "重新接入：valise enroll <服务器> --accept")
	}
	recipients, err := ReadRecipients(p)
	if err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return nil, valise.New("no_recipients", "服务器上还没有登记任何收件人公钥，无法加密", "在客户端运行 valise key register <服务器>")
	}
	defer func() {
		ent := AuditEntry{Time: time.Now().UTC(), Op: "seal", Instance: params.InstanceID, Initiator: initiator, Reason: params.Reason, Result: "ok"}
		if err != nil {
			ent.Result = "failed"
		} else {
			ent.SnapshotID, ent.Bytes = rep.SnapshotID, rep.AddedBytes
		}
		p.AppendAudit(ent)
	}()

	rpc.Report(ctx, vaultrpc.SealStageScan, 0, 0)
	provenance := rec.Provenance
	var runtimeRecord *vault.RuntimeRecord
	var programVerified bool
	if active == nil {
		runtimeRecord, err = captureRuntime(ctx, p.Home, rec, start)
	} else {
		runtimeRecord, provenance, programVerified, err = captureActiveRuntime(ctx, p.Home, active, start)
	}
	if err != nil {
		return nil, err
	}
	roots := make([]enroll.Root, 0, len(rec.Roots))
	for id, path := range rec.Roots {
		st, err := os.Stat(path)
		r := enroll.Root{ID: id, Path: path, Exists: err == nil && st.IsDir()}
		if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
			r.Resolved = real
		}
		roots = append(roots, r)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].ID < roots[j].ID })

	rep = &vaultrpc.SealReport{InstanceID: params.InstanceID, Tier: params.Tier, Categories: map[string]vaultrpc.Tally{},
		Unstable: []string{}, SQLite: []vault.SQLiteNote{}}
	var items []porterenroll.Item
	c := profile.NewClassifier(prof, rec.LocalOverrides)
	var nested []string
	if active == nil {
		nested = porterenroll.NestedInstances(e.For(prof), prof, params.InstanceID, rec.Roots[profile.StateRoot])
	} else {
		nested, err = activeNested(e, rec, prof)
		if err != nil {
			return nil, err
		}
	}
	overlay, hidden, expected, err := activeProgramOverlay(ctx, p.Home, active, params.Tier, programVerified, c)
	if err != nil {
		return nil, err
	}
	nested = append(nested, hidden...)
	porterenroll.Walk(roots, nested, c, func(it porterenroll.Item) {
		if hiddenProgramPath(it.Abs, hidden) {
			return
		}
		if it.SidecarOf != "" {
			return // covered by the database's VACUUM INTO copy
		}
		if it.Match.Category == "" {
			// Unknown means sensitive: sealed with the secrets layer only.
			it.Match.Category = vault.CategoryUnclassified
			if !it.Symlink {
				rep.UnclassifiedCount++
			}
		}
		items = append(items, it)
	}, func(root, rel string) {
		rep.Warnings = append(rep.Warnings, "无法读取 "+root+":"+rel+"（权限不足），未封存")
	})
	items = append(items, overlay...)

	// Preflight: room for the SQLite copies.
	var dbBytes uint64
	var packTotal int64
	for _, it := range items {
		if it.Symlink || !vault.InTier(params.Tier, it.Match.Category) {
			continue
		}
		packTotal += it.Info.Size()
		if it.SQLite != nil {
			dbBytes += uint64(it.Info.Size())
		}
	}
	if free, err := freeBytes(p.Tmp()); err == nil && free < dbBytes*2+(64<<20) {
		return nil, valise.New("tmp_space_low", "服务器上的临时目录空间不足，无法生成 SQLite 一致性副本",
			fmt.Sprintf("在 %s 所在的分区释放至少 %d MiB 空间后重试", p.Tmp(), (dbBytes*2>>20)+64))
	}

	leaf, err := leafForSeal(p, params.InstanceID, now())
	if err != nil {
		return nil, err
	}
	rep.Leaf = leaf.Leaf
	w, err := vault.OpenWriter(leaf.Dir, recipients, p.DedupeKey, time.Now())
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			w.Abort()
		}
	}()
	rep.Epoch = w.Epoch()
	var files vault.Files
	tally := func(cat string, n int64) {
		t := rep.Categories[cat]
		t.Files++
		t.Bytes += n
		rep.Categories[cat] = t
	}
	var packed int64
	var reported time.Time
	pack := func(n int64, last bool) {
		packed += n
		if last || time.Since(reported) > 200*time.Millisecond {
			reported = time.Now()
			rpc.Report(ctx, vaultrpc.SealStagePack, min(packed, packTotal), packTotal)
		}
	}
	pack(0, true)
	for _, it := range items {
		if entry, ok := expected[it.Abs]; ok {
			if !vault.InTier(params.Tier, it.Match.Category) {
				continue
			}
			f, link, err := sealProgramFile(w, it, entry)
			if err != nil {
				return nil, err
			}
			if link != nil {
				files.Symlinks = append(files.Symlinks, *link)
			} else {
				files.Files = append(files.Files, *f)
				tally(f.Category, f.Size)
				pack(f.Size, false)
			}
			continue
		}
		if it.Symlink {
			// Symlinks are classified like files: only those whose
			// category is in the tier are recorded (never followed).
			if vault.InTier(params.Tier, it.Match.Category) {
				if target, err := os.Readlink(it.Abs); err == nil {
					files.Symlinks = append(files.Symlinks, vault.Symlink{Root: it.Root, Path: it.Rel, Target: target})
				}
			}
			continue
		}
		if !vault.InTier(params.Tier, it.Match.Category) {
			continue
		}
		var f *vault.File
		if it.SQLite != nil {
			var note *vault.SQLiteNote
			f, note, err = sealSQLite(w, p, it, params.Tier)
			if err != nil {
				return nil, valise.New("sqlite_copy_failed", "无法生成数据库 "+it.Root+":"+it.Rel+" 的一致性副本", "确认数据库没有损坏；如果 harness 正在大量写入，稍后重试").WithDetail(err.Error())
			}
			rep.SQLite = append(rep.SQLite, *note)
		} else {
			var unstable bool
			f, unstable, err = sealFile(w, it)
			if err != nil {
				if os.IsNotExist(err) {
					rep.Warnings = append(rep.Warnings, it.Root+":"+it.Rel+" 在封存过程中被删除，未封存")
					err = nil
					continue
				}
				return nil, err
			}
			if unstable {
				rep.Unstable = append(rep.Unstable, it.Root+":"+it.Rel)
			}
		}
		files.Files = append(files.Files, *f)
		tally(f.Category, f.Size)
		pack(it.Info.Size(), false)
	}
	pack(0, true)

	if active != nil && !active.Native && rec.Provenance.Git != nil {
		rep.Warnings = append(rep.Warnings, "当前独立程序尚未适配 Git 改动记录；本次备份只保存所选数据范围")
	} else if rec.Provenance.Git != nil && rec.Provenance.Git.Path != "" {
		patch, perr := gitPatch(ctx, e, rec.Provenance.Git.Path)
		switch {
		case perr != nil:
			rep.Warnings = append(rep.Warnings, "无法生成 body_patch："+perr.Error())
		case len(patch) > 0:
			blobs, sum, n, err := w.AddStream(bytes.NewReader(patch))
			if err != nil {
				return nil, err
			}
			files.Files = append(files.Files, vault.File{Root: vault.BodyPatchRoot, Path: vault.BodyPatchPath, Category: profile.CategoryBodyPatch,
				Mode: 0o600, MTime: time.Now().UTC(), Size: n, SHA256: sum, Blobs: blobs})
			tally(profile.CategoryBodyPatch, n)
			rep.BodyPatch = true
		}
	}
	if err := validateActiveBackup(ctx, p.Home, active, programVerified && params.Tier == vault.TierRaw); err != nil {
		return nil, err
	}
	if active != nil && !active.Native && programVerified && params.Tier != vault.TierRaw {
		if err := (activeview.Reader{Home: p.Home}).VerifyProgram(ctx, active); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Preserve readable data even if programme bytes changed during
			// packing; do not preserve a now-stale source claim in its header.
			runtimeRecord, _ = runtimeLayout(p.Home, rec.Roots, start)
			runtimeRecord.Unresolved = []string{"active_program_unverified", "program_source_unverified", "runtime_dependencies_unverified"}
			provenance, programVerified = enroll.Provenance{}, false
		}
	}
	if active != nil && !active.Native && !programVerified {
		rep.Warnings = append(rep.Warnings, "已保存所选数据，但当前程序来源尚未核实；这份备份仍可取回数据，完整重建需要补齐程序来源")
	}
	if err := validateActiveBackup(ctx, p.Home, active, false); err != nil {
		return nil, err
	}

	rpc.Report(ctx, vaultrpc.SealStageCommit, 0, 0)
	now := time.Now().UTC()
	snap := vault.Snapshot{InstanceID: rec.InstanceID, Tier: params.Tier, CreatedAt: now,
		Profile:    vault.ProfileRef{ID: prof.ID, Version: prof.ProfileVersion, Status: prof.Status},
		ProfileDoc: prof, Provenance: provenance, LocalOverrides: rec.LocalOverrides, Roots: rec.Roots, SQLite: rep.SQLite,
		Name: rec.Name, Env: rec.Env, Runtime: runtimeRecord,
		Notes: vault.Notes{Unstable: rep.Unstable, UnclassifiedCount: rep.UnclassifiedCount}}
	meta, err := w.Commit(snap, files, vault.Meta{})
	if err != nil {
		return nil, err
	}
	rep.SnapshotID, rep.Files, rep.PlainBytes, rep.AddedBytes = meta.SnapshotID, meta.FileCount, meta.PlainBytes, meta.AddedBytes
	updateSeen(p, params.InstanceID, files.Files)
	rep.NewEpoch = w.NewEpoch()
	rep.DurationMS = time.Since(start).Milliseconds()
	return rep, nil
}

// sealFile reads a regular file, comparing size and mtime before and after;
// on a change it reads once more, and if it still changes the file is
// unstable (sealed as read the second time, not a failure).
func sealFile(w *vault.Writer, it porterenroll.Item) (*vault.File, bool, error) {
	for attempt := 0; ; attempt++ {
		before, err := os.Lstat(it.Abs)
		if err != nil {
			return nil, false, err
		}
		fh, err := os.Open(it.Abs)
		if err != nil {
			return nil, false, err
		}
		blobs, sum, n, err := w.AddStream(fh)
		fh.Close()
		if err != nil {
			return nil, false, err
		}
		after, err := os.Lstat(it.Abs)
		if err != nil {
			return nil, false, err
		}
		changed := before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || n != after.Size()
		if !changed || attempt == 1 {
			return &vault.File{Root: it.Root, Path: it.Rel, Category: it.Match.Category, Mode: uint32(after.Mode().Perm()),
				MTime: after.ModTime().UTC(), Size: n, SHA256: sum, Blobs: blobs}, changed, nil
		}
	}
}

// sealSQLite seals a VACUUM INTO copy; the soul tier strips declared secret
// tables in the copy.
func sealSQLite(w *vault.Writer, p Paths, it porterenroll.Item, tier string) (*vault.File, *vault.SQLiteNote, error) {
	if err := os.MkdirAll(p.Tmp(), 0o700); err != nil {
		return nil, nil, err
	}
	// A unique name for the copy: VACUUM INTO needs a path that does not
	// exist yet. Only this file (and a journal SQLite may leave) is removed.
	tmp, err := os.CreateTemp(p.Tmp(), "seal-*.sqlite")
	if err != nil {
		return nil, nil, err
	}
	dst := tmp.Name()
	tmp.Close()
	os.Remove(dst)
	defer func() {
		for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
			os.Remove(dst + suffix)
		}
	}()
	var strip []string
	if tier == vault.TierSoul {
		strip = it.SQLite.SecretTables
	}
	stripped, err := snapshotDB(it.Abs, dst, strip)
	if err != nil {
		return nil, nil, err
	}
	fh, err := os.Open(dst)
	if err != nil {
		return nil, nil, err
	}
	defer fh.Close()
	blobs, sum, n, err := w.AddStream(fh)
	if err != nil {
		return nil, nil, err
	}
	return &vault.File{Root: it.Root, Path: it.Rel, Category: it.Match.Category, Mode: uint32(it.Info.Mode().Perm()),
			MTime: it.Info.ModTime().UTC(), Size: n, SHA256: sum, Blobs: blobs},
		&vault.SQLiteNote{Root: it.Root, Path: it.Rel, StrippedTables: stripped}, nil
}

func gitPatch(ctx context.Context, e *porterenroll.Env, dir string) ([]byte, error) {
	st, err := e.Run(ctx, gitTimeout, dir, "git", "-C", dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil || st.Code != 0 {
		return nil, fmt.Errorf("git status failed")
	}
	if len(bytes.TrimSpace(st.Stdout)) == 0 {
		return nil, nil
	}
	out, err := e.Run(ctx, gitTimeout, dir, "git", "-C", dir, "diff", "HEAD", "--binary")
	if err != nil || out.Code != 0 {
		return nil, fmt.Errorf("git diff failed")
	}
	return out.Stdout, nil
}
