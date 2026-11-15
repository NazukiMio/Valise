package seal

import (
	"context"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/vault"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
)

// captureRuntime records registration and canonical-layout facts. It does not
// execute an interpreter, consult PATH, read credentials, mutate a dossier or
// claim that registration describes the program currently running.
func captureRuntime(ctx context.Context, home string, rec *enroll.Record, at time.Time) (*vault.RuntimeRecord, error) {
	missing := map[string]bool{"runtime_dependencies_unverified": true, "program_source_unverified": true}
	root := func(p string) vault.RuntimeRoot {
		r := vault.RuntimeRoot{Path: p}
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			missing["layout_unresolved"] = true
			return r
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			missing["layout_unresolved"] = true
		} else {
			r.Resolved = resolved
		}
		return r
	}
	roots := func(in map[string]string) map[string]vault.RuntimeRoot {
		out := map[string]vault.RuntimeRoot{}
		for id, p := range in {
			out[id] = root(p)
		}
		return out
	}
	r := &vault.RuntimeRecord{Format: vault.FormatRuntime, CapturedAt: at.UTC(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		Home: root(home), Roots: roots(rec.Roots)}
	d, p, err := porterenroll.ReadInstallation(home, rec.InstanceID)
	if err != nil || d == nil || p == nil || d.ProfileID != rec.ProfileID || d.Version != rec.ProfileVersion || p.ID != d.ProfileID || p.ProfileVersion != d.Version {
		missing["installation_record_unavailable"] = true
	} else {
		var defaults []enroll.DossierAgent
		for _, a := range d.Agents {
			if a.Name == "" && a.Superseded == nil {
				defaults = append(defaults, a)
			}
		}
		if len(defaults) != 1 {
			missing["installation_default_unresolved"] = true
		} else {
			a := defaults[0]
			r.Installation = &vault.RuntimeInstallation{InstallID: d.InstallID, DefaultInstanceID: a.InstanceID, AcceptedAt: a.EnrolledAt,
				ProfileDoc: p, Roots: roots(a.Roots), RegisteredProvenance: d.Provenance}
			if d.Provenance.Kind == "" || p.Provenance == nil {
				missing["registered_source_unavailable"] = true
			}
			if a.Roots[profile.StateRoot] == "" {
				missing["installation_layout_unresolved"] = true
			}
			if d.Provenance.Kind == profile.ProvenanceNPMGlobal && p.Body != nil {
				observed, err := environments.ObserveInstalledNPM(ctx, environments.InstalledSource{Profile: *p, Overrides: a.LocalOverrides,
					Layout: environments.Layout{Home: home, Roots: a.Roots}, Origin: d.Provenance, MaxBytes: 8 << 30})
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if err == nil {
					r.Dependencies = observed.Dependencies
					delete(missing, "program_source_unverified")
				} else {
					missing["program_tree_unavailable"] = true
				}
			}
		}
	}
	for code := range missing {
		r.Unresolved = append(r.Unresolved, code)
	}
	sort.Strings(r.Unresolved)
	return r, nil
}

// runtimeLayout has no installation-source fallback. It is also used when a
// managed programme cannot be verified but its active data is still readable.
func runtimeLayout(home string, paths map[string]string, at time.Time) (*vault.RuntimeRecord, error) {
	r := &vault.RuntimeRecord{Format: vault.FormatRuntime, CapturedAt: at.UTC(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		Roots: map[string]vault.RuntimeRoot{}}
	root := func(path string) vault.RuntimeRoot {
		out := vault.RuntimeRoot{Path: path}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			out.Resolved = real
		}
		return out
	}
	r.Home = root(home)
	for id, path := range paths {
		r.Roots[id] = root(path)
	}
	return r, nil
}
