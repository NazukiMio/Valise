package enroll

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"valise/core/enroll"
	"valise/core/valise"
)

// materializeLegacy is called only by existing explicit write operations,
// inside their mutation lock. It does not create identities or touch another
// installation. A committed record is never replaced with an old copy.
func materializeLegacy(home, id string) error {
	if d, err := dossierOf(home, id); err != nil || d != nil {
		return err
	}
	v, err := legacyView(home)
	if err != nil {
		return err
	}
	if err := v.issues[id]; err != nil {
		return err
	}
	legacy, general := v.find(id)
	if legacy == nil {
		return nil
	}
	d, err := readDossier(home, legacy.InstallID)
	if err != nil {
		return err
	}
	if d == nil {
		d = &enroll.Dossier{InstallID: legacy.InstallID}
	} else if d.Version >= general.ProfileVersion && len(d.Agents) > 0 {
		general, err = readDossierProfile(home, d.InstallID)
		if err != nil {
			return err
		}
	}
	committed, err := ListAcceptedRecords(home)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, r := range committed {
		known[r.InstanceID] = true
	}
	var imports []enroll.Record
	for _, a := range legacy.Agents {
		if known[a.InstanceID] {
			continue
		}
		r := *legacy.Record(a.InstanceID)
		r.ProfileVersion = general.ProfileVersion
		imports = append(imports, r)
	}
	// Check the complete conversion scope before its first write. In particular,
	// importing an old sibling must not upgrade an occupied shared profile.
	for _, r := range imports {
		d.Put(r, time.Now())
	}
	if err := writeDossierProfile(home, d.InstallID, *general); err != nil {
		return err
	}
	if err := writeDossier(home, d); err != nil {
		return err
	}
	for _, r := range imports {
		if err := archiveLegacy(home, r.InstanceID); err != nil {
			return err
		}
	}
	return nil
}

// archiveLegacy also handles an ignored duplicate on explicit removal. The
// exclusive link preserves prior archives without a check-then-rename overwrite;
// a crash may leave two names, which remain safe to read/retry.
func archiveLegacy(home, id string) (err error) {
	defer func() {
		if err != nil {
			if _, ok := valise.As(err); !ok {
				err = valise.New("legacy_archive_failed", "未能完整保留旧接入记录，本次修改尚未完成", "核对接入状态，检查服务器磁盘空间和记录目录权限后重试").WithDetail(err.Error())
			}
		}
	}()
	if err := enroll.ValidInstanceID(id); err != nil {
		return err
	}
	for _, pair := range [][2]string{{id + ".json", id + ".legacy"}, {id + ".profile.json", id + ".legacy-profile"}} {
		source := filepath.Join(InstancesDir(home), pair[0])
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return valise.New("legacy_archive_failed", "无法保留这份旧接入记录，尚未完成本次修改", "检查旧接入记录的文件类型和权限，再重试本次操作")
		}
		for n := 1; ; n++ {
			name := pair[1] + ".json"
			if n > 1 {
				name = fmt.Sprintf("%s.%d.json", pair[1], n)
			}
			err := os.Link(source, filepath.Join(InstancesDir(home), name))
			if errors.Is(err, os.ErrExist) {
				continue
			}
			if err != nil {
				return err
			}
			if err := os.Remove(source); err != nil {
				return err
			}
			break
		}
	}
	f, err := os.Open(InstancesDir(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
