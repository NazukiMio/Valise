// Package profiles loads the official profiles from the installed, signed
// profile pack and the user's local profiles, and lints them as one set.
// The client ships no profile of its own (docs/PROFILES.md 1.1).
package profiles

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"valise/core/kittools"

	"valise/core/profile"
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
	// Pack is the installed pack's manifest, nil when none is installed or
	// it failed verification (then PackProblem says why).
	Pack        *Manifest
	PackProblem error
	// Kit is the signed pack's Kit tool list (nil without one); local
	// profiles never add tools.
	Kit *kittools.List
}

// LocalDir is os.UserConfigDir()/valise/profiles.
func LocalDir(dataDir string) string { return filepath.Join(dataDir, "profiles") }

// Load reads the installed pack (verified against publicKey) and *.yaml in
// the local profile directory. A pack that fails verification is left out
// and reported in PackProblem; a failing local profile is skipped and
// reported in Rejected.
func Load(dataDir, publicKey string) (*Set, error) {
	var docs []*profile.Document
	local := map[*profile.Document]bool{}
	set := &Set{ByID: map[string]*Loaded{}}
	pack, err := Installed(dataDir, publicKey)
	if err != nil {
		set.PackProblem = err
	} else if pack != nil {
		set.Pack, set.Kit = &pack.Manifest, pack.Kit
		docs = append(docs, pack.Docs...)
	}
	localDir := LocalDir(dataDir)
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
				return nil, errors.New("资料包中的 profile 未通过 lint：" + strings.Join(msgs, "; "))
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
