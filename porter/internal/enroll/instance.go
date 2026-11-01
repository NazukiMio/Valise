package enroll

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
)

// InstancesDir is ~/.config/valise/instances.
func InstancesDir(home string) string {
	return filepath.Join(home, ".config", "valise", "instances")
}

// ReadRecord returns nil when the instance is not enrolled.
func ReadRecord(home, id string) (*enroll.Record, error) {
	if err := enroll.ValidInstanceID(id); err != nil {
		return nil, valise.New("instance_id_invalid", err.Error(), "使用合法的实例 id")
	}
	b, err := os.ReadFile(filepath.Join(InstancesDir(home), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r enroll.Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, valise.New("instance_record_corrupt", "服务器上的实例记录无法解析："+id, "重新登记以覆盖它").WithDetail(err.Error())
	}
	return &r, nil
}

// RecordPath is instances/<id>.json.
func RecordPath(home, id string) string {
	return filepath.Join(InstancesDir(home), id+".json")
}

// ProfilePath is instances/<id>.profile.json.
func ProfilePath(home, id string) string {
	return filepath.Join(InstancesDir(home), id+".profile.json")
}

// ReadProfile returns the profile stored at enrollment.
func ReadProfile(home, id string) (*profile.Profile, error) {
	b, err := os.ReadFile(ProfilePath(home, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, valise.New("instance_profile_missing", "服务器上没有实例 "+id+" 登记时的 profile", "重新登记：valise enroll <服务器> --accept")
	}
	if err != nil {
		return nil, err
	}
	var p profile.Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, valise.New("instance_profile_corrupt", "服务器上的 profile 副本无法解析："+id, "重新登记以覆盖它")
	}
	return &p, nil
}

// Refresh replaces the facts in rec that describe the server it lives on
// with what this server shows: the body as installed here, and service and
// entry state. A migrate or clone record starts from the snapshot, which
// describes the source server (its npm prefix, its paths). Provenance is
// replaced only when this server shows the same kind of body.
func Refresh(ctx context.Context, e *Env, p *profile.Profile, rec *enroll.Record) {
	e = e.For(p)
	roots := make([]enroll.Root, 0, len(rec.Roots))
	for id, path := range rec.Roots {
		roots = append(roots, enroll.Root{ID: id, Path: path, Exists: true})
	}
	if pv := Provenance(ctx, e, p, roots); pv.Kind == rec.Provenance.Kind {
		rec.Provenance = pv
	}
	var warns []string
	rec.Service = services(ctx, e, p, &warns)
	rec.Entries = entries(p)
}

// Live reads the current service and entry state of a profile on this
// server (read-only).
func Live(ctx context.Context, e *Env, p *profile.Profile) ([]enroll.ServiceState, []enroll.EntryState) {
	e = e.For(p)
	var warns []string
	return services(ctx, e, p, &warns), entries(p)
}

// WriteRecord writes the record and the profile atomically with mode 0600.
func WriteRecord(home string, r enroll.Record, p profile.Profile) (string, error) {
	if err := enroll.ValidInstanceID(r.InstanceID); err != nil {
		return "", valise.New("instance_id_invalid", err.Error(), "使用合法的实例 id")
	}
	dir := InstancesDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if p.ID == "" || p.ProfileVersion != r.ProfileVersion {
		return "", valise.New("instance_profile_mismatch", "实例记录与 profile 版本不一致", "重新运行登记")
	}
	pb, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeFile0600(dir, ProfilePath(home, r.InstanceID), pb); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	final := filepath.Join(dir, r.InstanceID+".json")
	return final, writeFile0600(dir, final, b)
}

func writeFile0600(dir, final string, b []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), final)
}

// ListRecords returns every instance record on this server, sorted by id.
// Unreadable records are skipped.
func ListRecords(home string) ([]enroll.Record, error) {
	ents, err := os.ReadDir(InstancesDir(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []enroll.Record
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".profile.json") {
			continue
		}
		if r, err := ReadRecord(home, strings.TrimSuffix(name, ".json")); err == nil && r != nil {
			out = append(out, *r)
		}
	}
	return out, nil
}

// MarkSuperseded records that an operation replaced the instance's
// directories. The record is kept; only `valise old cleanup` removes it.
func MarkSuperseded(home, id string, s enroll.Supersession) error {
	r, err := ReadRecord(home, id)
	if err != nil || r == nil {
		return err
	}
	r.Superseded = &s
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFile0600(InstancesDir(home), RecordPath(home, id), b)
}

// RemoveRecord deletes the record and profile copy of an instance. It runs
// only on the user's explicit `valise old cleanup`.
func RemoveRecord(home, id string) error {
	if err := enroll.ValidInstanceID(id); err != nil {
		return valise.New("instance_id_invalid", err.Error(), "使用合法的实例 id")
	}
	for _, p := range []string{RecordPath(home, id), ProfilePath(home, id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
