package enroll

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"valise/core/enroll"
	"valise/core/profile"
)

type legacyViews struct {
	dossiers map[string]*enroll.Dossier
	profiles map[string]profile.Profile
	issues   map[string]error
}

// legacyView projects the old format without publishing installation facts.
// Committed callers take precedence by instance ID before using this view.
func legacyView(home string) (legacyViews, error) {
	v := legacyViews{dossiers: map[string]*enroll.Dossier{}, profiles: map[string]profile.Profile{}, issues: map[string]error{}}
	entries, err := os.ReadDir(InstancesDir(home))
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	committed, err := allDossiers(home)
	if err != nil {
		return v, err
	}
	known := map[string]bool{}
	for _, d := range committed {
		for _, a := range d.Agents {
			known[a.InstanceID] = true
		}
	}
	type item struct {
		r enroll.Record
		p profile.Profile
	}
	var items []item
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || strings.Contains(id, ".") || enroll.ValidInstanceID(id) != nil || known[id] {
			continue
		}
		var r enroll.Record
		var p profile.Profile
		if legacyJSON(filepath.Join(InstancesDir(home), entry.Name()), &r) != nil || r.InstanceID != id || enroll.ValidInstanceID(r.ProfileID) != nil ||
			legacyJSON(filepath.Join(InstancesDir(home), id+".profile.json"), &p) != nil || p.ID != r.ProfileID {
			continue // Preserve the existing listing behavior for broken old pairs.
		}
		if _, err := readDossier(home, r.ProfileID); err != nil {
			v.issues[id] = err // A broken committed dossier cannot fall back to old facts.
			continue
		}
		items = append(items, item{r, p})
	}
	// A named legacy copy may lack shared capabilities; on equal versions the
	// default Agent's general profile wins, independent of filename order.
	sort.SliceStable(items, func(i, j int) bool { return items[i].r.Name == "" && items[j].r.Name != "" })
	for _, it := range items {
		r, p := it.r, it.p
		d := v.dossiers[p.ID]
		if d == nil {
			d = &enroll.Dossier{InstallID: p.ID}
			v.dossiers[p.ID] = d
		}
		if d.Version < p.ProfileVersion || len(d.Agents) == 0 || r.Name == "" && d.Version == p.ProfileVersion {
			v.profiles[p.ID] = p
		}
		r.ProfileVersion = v.profiles[p.ID].ProfileVersion
		d.Put(r, r.EnrolledAt)
	}
	return v, nil
}

func legacyJSON(path string, value any) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return errors.New("invalid legacy metadata")
	}
	b, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(b) > 2<<20 {
		return errors.New("legacy metadata too large")
	}
	return json.Unmarshal(b, value)
}

func (v legacyViews) find(id string) (*enroll.Dossier, *profile.Profile) {
	for install, d := range v.dossiers {
		if d.Agent(id) != nil {
			p := v.profiles[install]
			return d, &p
		}
	}
	return nil, nil
}
