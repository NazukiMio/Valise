package enroll

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
)

// InstancesDir is ~/.config/valise/instances.
func InstancesDir(home string) string {
	return filepath.Join(home, ".config", "valise", "instances")
}

// ListAcceptedRecords reads committed dossiers only. Daily managed consumers
// must not import old metadata while using an already bound profile/root set.
func ListAcceptedRecords(home string) ([]enroll.Record, error) {
	entries, err := os.ReadDir(DossiersDir(home))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []enroll.Record
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".profile.json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		d, err := readDossier(home, id)
		if err != nil {
			return nil, err
		}
		if d == nil {
			continue
		}
		for _, a := range d.Agents {
			if rec := d.Record(a.InstanceID); rec != nil {
				out = append(out, *rec)
			}
		}
	}
	return out, nil
}

// ReadRecord returns nil when the instance is not enrolled. It is a view
// of the agent in its instance dossier (docs/ARCHITECTURE.md 4.1).
func ReadRecord(home, id string) (*enroll.Record, error) {
	if err := enroll.ValidInstanceID(id); err != nil {
		return nil, valise.New("instance_id_invalid", err.Error(), "使用合法的实例 id")
	}
	d, err := dossierOf(home, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		v, err := legacyView(home)
		if err != nil {
			return nil, err
		}
		if err := v.issues[id]; err != nil {
			return nil, err
		}
		d, _ = v.find(id)
		if d == nil {
			return nil, nil
		}
	}
	return d.Record(id), nil
}

// ReadProfile returns the agent's effective profile: the general profile
// stored at enrollment, made for this agent (profile.ForAgent) with its
// entry ports.
func ReadProfile(home, id string) (*profile.Profile, error) {
	d, err := dossierOf(home, id)
	if err != nil {
		return nil, err
	}
	var g *profile.Profile
	if d == nil {
		v, e := legacyView(home)
		if e != nil {
			return nil, e
		}
		if e := v.issues[id]; e != nil {
			return nil, e
		}
		d, g = v.find(id)
		if d == nil {
			return nil, valise.New("instance_profile_missing", "服务器上没有实例 "+id+" 接入时的 profile", "重新接入：valise enroll <服务器> --accept")
		}
	} else {
		g, err = readDossierProfile(home, d.InstallID)
	}
	if err != nil {
		return nil, err
	}
	rec := d.Record(id)
	p := g.ForAgent(rec.Name)
	rec.ApplyEntryPorts(&p)
	return &p, nil
}

// SetEntryPort records port for one entry of an enrolled instance. The
// profile's own port stays the default; setting it back removes the
// override.
func SetEntryPort(home, id, entry string, port int) (map[string]int, error) {
	if port < 1 || port > 65535 {
		return nil, valise.New("entry_port_invalid", "端口必须在 1 到 65535 之间", "")
	}
	if err := materializeLegacy(home, id); err != nil {
		return nil, err
	}
	d, err := dossierOf(home, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+id, "先运行 valise enroll <服务器> --accept")
	}
	g, err := readDossierProfile(home, d.InstallID)
	if err != nil {
		return nil, err
	}
	a := d.Agent(id)
	def := 0
	for _, e := range g.ForAgent(a.Name).Entries {
		if e.ID == entry {
			def = e.Port
		}
	}
	if def == 0 {
		return nil, valise.New("entry_unknown", "实例 "+id+" 没有入口 "+entry, "")
	}
	if a.EntryPorts == nil {
		a.EntryPorts = map[string]int{}
	}
	a.EntryPorts[entry] = port
	if port == def {
		delete(a.EntryPorts, entry)
	}
	if err := writeDossier(home, d); err != nil {
		return nil, err
	}
	if a.EntryPorts == nil {
		a.EntryPorts = map[string]int{}
	}
	return a.EntryPorts, nil
}

// SetDisplayName sets the name the user gives agent id in Valise ("" to
// follow the harness's name).
func SetDisplayName(home, id, name string) error {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 40 || strings.ContainsAny(name, "\n\r\t") {
		return valise.New("display_name_invalid", "名字最长 40 个字，不能换行", "")
	}
	if err := materializeLegacy(home, id); err != nil {
		return err
	}
	d, err := dossierOf(home, id)
	if err != nil {
		return err
	}
	if d == nil {
		return valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+id, "先运行 valise enroll <服务器> --accept")
	}
	d.Agent(id).DisplayName = name
	return writeDossier(home, d)
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

// WriteRecord stores the agent's record and the general profile in the
// installation's dossier (written atomically, 0600). It returns the
// dossier's path.
func WriteRecord(home string, r enroll.Record, p profile.Profile) (string, error) {
	if err := enroll.ValidInstanceID(r.InstanceID); err != nil {
		return "", valise.New("instance_id_invalid", err.Error(), "使用合法的实例 id")
	}
	if p.ID == "" || p.ProfileVersion != r.ProfileVersion || p.ID != r.ProfileID {
		return "", valise.New("instance_profile_mismatch", "实例记录与 profile 版本不一致", "重新接入")
	}
	if err := materializeLegacy(home, r.InstanceID); err != nil {
		return "", err
	}
	d, err := dossierOf(home, r.InstanceID)
	if err != nil {
		return "", err
	}
	if d != nil && d.InstallID != p.ID {
		// The agent now belongs to another installation.
		if !d.Remove(r.InstanceID) {
			if err := removeDossier(home, d.InstallID); err != nil {
				return "", err
			}
		} else if err := writeDossier(home, d); err != nil {
			return "", err
		}
		d = nil
	}
	if d == nil {
		if d, err = readDossier(home, p.ID); err != nil {
			return "", err
		}
		if d == nil {
			d = &enroll.Dossier{InstallID: p.ID}
		}
	}
	d.Put(r, time.Now())
	if err := writeDossierProfile(home, d.InstallID, p); err != nil {
		return "", err
	}
	if err := writeDossier(home, d); err != nil {
		return "", err
	}
	// Also finish an interrupted conversion whose dossier was committed before
	// its old files were archived. Queries still leave those duplicates alone.
	return DossierPath(home, d.InstallID), archiveLegacy(home, r.InstanceID)
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

// ListRecords returns every agent's record on this server, sorted by id.
// Unreadable dossiers are skipped.
func ListRecords(home string) ([]enroll.Record, error) {
	ds, err := allDossiers(home)
	if err != nil {
		return nil, err
	}
	var out []enroll.Record
	seen := map[string]bool{}
	for _, d := range ds {
		for _, a := range d.Agents {
			out = append(out, *d.Record(a.InstanceID))
			seen[a.InstanceID] = true
		}
	}
	legacy, err := legacyView(home)
	if err != nil {
		return nil, err
	}
	for _, d := range legacy.dossiers {
		for _, a := range d.Agents {
			if !seen[a.InstanceID] {
				out = append(out, *d.Record(a.InstanceID))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
	return out, nil
}

// MarkSuperseded records that an operation replaced the instance's
// directories. The agent is kept; only `valise old cleanup` removes it.
func MarkSuperseded(home, id string, s enroll.Supersession) error {
	if err := materializeLegacy(home, id); err != nil {
		return err
	}
	d, err := dossierOf(home, id)
	if err != nil || d == nil {
		return err
	}
	d.Agent(id).Superseded = &s
	return writeDossier(home, d)
}

// RemoveRecord removes an agent from its dossier (the dossier and its
// profile copy go with the last agent). It runs only on the user's
// explicit `valise old cleanup`.
func RemoveRecord(home, id string) error {
	if err := enroll.ValidInstanceID(id); err != nil {
		return valise.New("instance_id_invalid", err.Error(), "使用合法的实例 id")
	}
	if err := materializeLegacy(home, id); err != nil {
		return err
	}
	d, err := dossierOf(home, id)
	if err != nil || d == nil {
		return err
	}
	if err := archiveLegacy(home, id); err != nil {
		return err
	}
	if !d.Remove(id) {
		return removeDossier(home, d.InstallID)
	}
	return writeDossier(home, d)
}
