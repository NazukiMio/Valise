// Package profiles loads the official profiles from the installed, signed
// profile pack and the user's local profiles, and lints them as one set.
// The client ships no profile of its own (docs/PROFILES.md 1.1).
package profiles

import (
	"errors"
	"fmt"
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
	// OverridesOfficial is true when a local profile overrides an official
	// profile from the installed signed pack; Official is the overridden
	// official effective profile.
	OverridesOfficial bool
	Official          *profile.Profile
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
// reported in Rejected. A valid local profile whose id matches an official
// pack profile overrides it while preserving Official on Loaded.
func Load(dataDir, publicKey string) (*Set, error) {
	var packDocs []*profile.Document
	officialEffByID := map[string]*profile.Profile{}
	set := &Set{ByID: map[string]*Loaded{}}
	pack, err := Installed(dataDir, publicKey)
	if err != nil {
		set.PackProblem = err
	} else if pack != nil {
		set.Pack, set.Kit = &pack.Manifest, pack.Kit
		packDocs = append(packDocs, pack.Docs...)
		for _, r := range profile.Lint(packDocs) {
			if len(r.Issues) > 0 {
				var msgs []string
				for _, i := range r.Issues {
					msgs = append(msgs, i.String())
				}
				return nil, errors.New("资料包中的 profile 未通过 lint：" + strings.Join(msgs, "; "))
			}
			officialEffByID[r.Doc.Profile.ID] = r.Effective
		}
	}
	localDir := LocalDir(dataDir)
	files, _ := filepath.Glob(filepath.Join(localDir, "*.yaml"))
	sort.Strings(files)
	var localDocs []*profile.Document
	localByID := map[string]*profile.Document{}
	localSet := map[*profile.Document]bool{}
	for _, f := range files {
		d, err := profile.LoadFile(f)
		if err != nil {
			set.Rejected = append(set.Rejected, err.Error())
			continue
		}
		id := d.Profile.ID
		if id != "" {
			if prev, dup := localByID[id]; dup {
				set.Rejected = append(set.Rejected, fmt.Sprintf("%s: id: id %q 与 %s 重复", d.Path, id, prev.Path))
				continue
			}
			localByID[id] = d
		}
		localDocs = append(localDocs, d)
		localSet[d] = true
	}

	for {
		docs := make([]*profile.Document, 0, len(packDocs)+len(localDocs))
		replacedOfficial := map[string]bool{}
		for _, od := range packDocs {
			if ld, ok := localByID[od.Profile.ID]; ok {
				docs = append(docs, ld)
				replacedOfficial[od.Profile.ID] = true
			} else {
				docs = append(docs, od)
			}
		}
		for _, ld := range localDocs {
			if !replacedOfficial[ld.Profile.ID] {
				docs = append(docs, ld)
			}
		}
		results := profile.Lint(docs)
		rejectedAny := false
		for _, r := range results {
			if len(r.Issues) == 0 {
				continue
			}
			var msgs []string
			for _, i := range r.Issues {
				msgs = append(msgs, i.String())
			}
			target := r.Doc
			if !localSet[target] {
				// Official pack docs were already verified above; if an official
				// child fails now, a local override of its parent broke it.
				if parent, ok := localByID[target.Profile.Extends]; ok {
					target = parent
				} else {
					return nil, errors.New("资料包中的 profile 未通过 lint：" + strings.Join(msgs, "; "))
				}
			}
			set.Rejected = append(set.Rejected, strings.Join(msgs, "; "))
			delete(localByID, target.Profile.ID)
			nextLocal := localDocs[:0]
			for _, ld := range localDocs {
				if ld != target {
					nextLocal = append(nextLocal, ld)
				}
			}
			localDocs = nextLocal
			rejectedAny = true
		}
		if !rejectedAny {
			for _, r := range results {
				id := r.Doc.Profile.ID
				isLocal := localSet[r.Doc]
				var official *profile.Profile
				overrides := false
				if isLocal {
					if eff, ok := officialEffByID[id]; ok {
						overrides = true
						official = eff
					}
				}
				set.ByID[id] = &Loaded{
					Doc:               r.Doc,
					Effective:         r.Effective,
					Local:             isLocal,
					OverridesOfficial: overrides,
					Official:          official,
				}
			}
			break
		}
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
