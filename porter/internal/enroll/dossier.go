package enroll

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
)

// Instance dossiers (docs/ARCHITECTURE.md 4.1): one file per harness
// installation with the facts of this server, beside an unchanged copy of
// the general profile.

// DossiersDir is ~/.config/valise/dossiers.
func DossiersDir(home string) string { return filepath.Join(home, ".config", "valise", "dossiers") }

// DossierPath is dossiers/<install>.json.
func DossierPath(home, install string) string {
	return filepath.Join(DossiersDir(home), install+".json")
}

func dossierProfilePath(home, install string) string {
	return filepath.Join(DossiersDir(home), install+".profile.json")
}

func readDossier(home, install string) (*enroll.Dossier, error) {
	b, err := os.ReadFile(DossierPath(home, install))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d enroll.Dossier
	if err := json.Unmarshal(b, &d); err != nil || d.Schema != enroll.DossierSchema {
		return nil, valise.New("dossier_corrupt", "服务器上的实例档案无法解析："+install, "重新接入以覆盖它")
	}
	return &d, nil
}

func writeDossier(home string, d *enroll.Dossier) error {
	dir := DossiersDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if d.Agents == nil {
		d.Agents = []enroll.DossierAgent{}
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return writeFile0600(dir, DossierPath(home, d.InstallID), b)
}

func readDossierProfile(home, install string) (*profile.Profile, error) {
	b, err := os.ReadFile(dossierProfilePath(home, install))
	if errors.Is(err, os.ErrNotExist) {
		return nil, valise.New("instance_profile_missing", "服务器上没有 "+install+" 接入时的 profile", "重新接入：valise enroll <服务器> --accept")
	}
	if err != nil {
		return nil, err
	}
	var p profile.Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, valise.New("instance_profile_corrupt", "服务器上的 profile 副本无法解析："+install, "重新接入以覆盖它")
	}
	return &p, nil
}

func writeDossierProfile(home, install string, p profile.Profile) error {
	dir := DossiersDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writeFile0600(dir, dossierProfilePath(home, install), b)
}

// removeDossier deletes a dossier that has no agent left, with its
// profile copy (on the user's explicit cleanup only).
func removeDossier(home, install string) error {
	for _, p := range []string{DossierPath(home, install), dossierProfilePath(home, install)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// allDossiers reads every dossier, first moving legacy instance records
// (instances/<id>.json with <id>.profile.json) into them.
func allDossiers(home string) ([]*enroll.Dossier, error) {
	if err := migrateLegacy(home); err != nil {
		return nil, err
	}
	names, _ := filepath.Glob(filepath.Join(DossiersDir(home), "*.json"))
	sort.Strings(names)
	var out []*enroll.Dossier
	for _, n := range names {
		if strings.HasSuffix(n, ".profile.json") {
			continue
		}
		if d, err := readDossier(home, strings.TrimSuffix(filepath.Base(n), ".json")); err == nil && d != nil {
			out = append(out, d)
		}
	}
	return out, nil
}

// dossierOf finds the dossier holding agent id, nil when none.
func dossierOf(home, id string) (*enroll.Dossier, error) {
	ds, err := allDossiers(home)
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		if d.Agent(id) != nil {
			return d, nil
		}
	}
	return nil, nil
}

// InstallOf is the installation holding agent id, "" when none.
func InstallOf(home, id string) string {
	if d, err := dossierOf(home, id); err == nil && d != nil {
		return d.InstallID
	}
	return ""
}

// migrateLegacy moves each legacy record into its installation's dossier
// and renames the old files to <id>.legacy.json and <id>.legacy-profile.json
// (kept, not deleted).
func migrateLegacy(home string) error {
	ents, err := os.ReadDir(InstancesDir(home))
	if err != nil {
		return nil
	}
	dir := InstancesDir(home)
	type legacy struct {
		id string
		r  enroll.Record
	}
	var all []legacy
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || strings.Contains(id, ".") || enroll.ValidInstanceID(id) != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, id+".json"))
		if err != nil {
			continue
		}
		var r enroll.Record
		if json.Unmarshal(b, &r) != nil || r.ProfileID == "" {
			continue
		}
		all = append(all, legacy{id, r})
	}
	// Default agents first: a named agent's legacy profile copy may lack
	// the service, so the installation's copy comes from the default one.
	sort.SliceStable(all, func(i, j int) bool { return all[i].r.Name == "" && all[j].r.Name != "" })
	for _, l := range all {
		id, r := l.id, l.r
		pb, err := os.ReadFile(filepath.Join(dir, id+".profile.json"))
		if err != nil {
			continue
		}
		var p profile.Profile
		if json.Unmarshal(pb, &p) != nil || p.ID != r.ProfileID {
			continue
		}
		d, err := readDossier(home, p.ID)
		if err != nil {
			return err
		}
		if d == nil {
			d = &enroll.Dossier{InstallID: p.ID}
		}
		if d.Agent(id) == nil {
			// The newest profile version is the installation's.
			if d.Version < p.ProfileVersion || len(d.Agents) == 0 || (r.Name == "" && d.Version == p.ProfileVersion) {
				if err := writeDossierProfile(home, d.InstallID, p); err != nil {
					return err
				}
			} else {
				r.ProfileVersion = d.Version
			}
			d.Put(r, time.Now())
			if err := writeDossier(home, d); err != nil {
				return err
			}
		}
		os.Rename(filepath.Join(dir, id+".json"), filepath.Join(dir, id+".legacy.json"))
		os.Rename(filepath.Join(dir, id+".profile.json"), filepath.Join(dir, id+".legacy-profile.json"))
	}
	return nil
}
