// Package profiles loads the official profiles embedded in the client and
// the user's local profiles, and lints them as one set.
package profiles

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"valise/core/profile"
	official "valise/profiles"
)

// Loaded is a profile ready to use.
type Loaded struct {
	Doc       *profile.Document
	Effective *profile.Profile
	// Local profiles come from the user's directory and are never reviewed.
	Local bool
}

// Set is every usable profile, keyed by id.
type Set struct {
	ByID map[string]*Loaded
	// Rejected lists local profiles that failed lint, with reasons.
	Rejected []string
}

// LocalDir is os.UserConfigDir()/valise/profiles.
func LocalDir(dataDir string) string { return filepath.Join(dataDir, "profiles") }

// Load reads the embedded official profiles and *.yaml in localDir (which
// may be missing). An official profile failing lint is a build defect and an
// error; a failing local profile is skipped and reported in Rejected.
func Load(localDir string) (*Set, error) {
	var docs []*profile.Document
	local := map[*profile.Document]bool{}
	names, _ := fs.Glob(official.FS, "*.yaml")
	for _, n := range names {
		b, err := official.FS.ReadFile(n)
		if err != nil {
			return nil, err
		}
		d, err := profile.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("内置 profile %s 无法解析：%w", n, err)
		}
		d.Path = "profiles/" + n
		docs = append(docs, d)
	}
	set := &Set{ByID: map[string]*Loaded{}}
	files, _ := filepath.Glob(filepath.Join(localDir, "*.yaml"))
	sort.Strings(files)
	for _, f := range files {
		d, err := profile.LoadFile(f)
		if err != nil {
			set.Rejected = append(set.Rejected, err.Error())
			continue
		}
		docs = append(docs, d)
		local[d] = true
	}
	for _, r := range profile.Lint(docs) {
		if len(r.Issues) > 0 {
			var msgs []string
			for _, i := range r.Issues {
				msgs = append(msgs, i.String())
			}
			if !local[r.Doc] {
				return nil, errors.New("内置 profile 未通过 lint：" + strings.Join(msgs, "; "))
			}
			set.Rejected = append(set.Rejected, strings.Join(msgs, "; "))
			continue
		}
		set.ByID[r.Doc.Profile.ID] = &Loaded{Doc: r.Doc, Effective: r.Effective, Local: local[r.Doc]}
	}
	return set, nil
}

// Effective returns every effective profile, sorted by id, to send to porter.
func (s *Set) Effective() []profile.Profile {
	ids := make([]string, 0, len(s.ByID))
	for id := range s.ByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]profile.Profile, 0, len(ids))
	for _, id := range ids {
		out = append(out, *s.ByID[id].Effective)
	}
	return out
}
