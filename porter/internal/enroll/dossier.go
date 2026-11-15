package enroll

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

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

// allDossiers reads only committed installation dossiers. Queries never import
// legacy metadata; ReadRecord/ReadProfile/ListRecords provide a read-only view.
func allDossiers(home string) ([]*enroll.Dossier, error) {
	names, _ := filepath.Glob(filepath.Join(DossiersDir(home), "*.json"))
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
	var found *enroll.Dossier
	for _, d := range ds {
		if d.Agent(id) != nil {
			if found != nil {
				return nil, valise.New("installation_ambiguous", "这个 Agent 出现在多份接入档案中", "重新接入并核对它属于哪一份安装")
			}
			found = d
		}
	}
	return found, nil
}

// InstallOf is the installation holding agent id, "" when none.
func InstallOf(home, id string) string {
	if d, err := dossierOf(home, id); err == nil && d != nil {
		return d.InstallID
	}
	if v, err := legacyView(home); err == nil {
		if d, _ := v.find(id); d != nil {
			return d.InstallID
		}
	}
	return ""
}

// ReadInstallation returns the saved installation dossier and its general
// profile. Unlike ReadRecord/ReadProfile, this does not turn installation facts
// into named-Agent capabilities or replace its deliberately empty provenance.
func ReadInstallation(home, id string) (*enroll.Dossier, *profile.Profile, error) {
	if err := enroll.ValidInstanceID(id); err != nil {
		return nil, nil, err
	}
	// Do not invoke the legacy migration path here: runtime collection is
	// metadata-only. Legacy records remain separate until an explicit mutation.
	names, err := filepath.Glob(filepath.Join(DossiersDir(home), "*.json"))
	if err != nil {
		return nil, nil, err
	}
	var d *enroll.Dossier
	for _, name := range names {
		if strings.HasSuffix(name, ".profile.json") {
			continue
		}
		install := strings.TrimSuffix(filepath.Base(name), ".json")
		candidate, err := readDossier(home, install)
		if err != nil || candidate == nil || candidate.InstallID != install || enroll.ValidInstanceID(install) != nil || candidate.Agent(id) == nil {
			continue
		}
		if d != nil {
			return nil, nil, valise.New("installation_ambiguous", "这个 Agent 出现在多份接入档案中", "重新接入并核对它属于哪一份安装")
		}
		d = candidate
	}
	if d == nil {
		return nil, nil, nil
	}
	p, err := readDossierProfile(home, d.InstallID)
	if err != nil {
		return nil, nil, err
	}
	return d, p, nil
}
