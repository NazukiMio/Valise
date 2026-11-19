package mcp

import (
	"cmp"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/kit"
	porterprotection "valise/porter/internal/protection"
	"valise/porter/internal/seal"
)

const (
	onboardFactLimit = 20
	sqliteMagic      = "SQLite format 3\x00"
)

type onboardInstance struct {
	InstanceID     string            `json:"instance_id"`
	ProfileID      string            `json:"profile_id"`
	ProfileVersion int               `json:"profile_version"`
	ProfileStatus  string            `json:"profile_status"`
	Roots          map[string]string `json:"roots"`
}

type onboardEffectiveRules struct {
	ProfilePaths   []profile.PathRule   `json:"profile_paths"`
	ProfileSQLite  []profile.SQLiteRef  `json:"profile_sqlite,omitempty"`
	LocalOverrides []profile.Override   `json:"local_overrides"`
	LocalSQLite    []profile.SQLiteRef  `json:"local_sqlite,omitempty"`
	ExternalRefs   []enroll.ExternalRef `json:"external_refs,omitempty"`
}

type dirStat struct {
	Root   string `json:"root"`
	Prefix string `json:"prefix"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
}

type symlinkFact struct {
	Root       string `json:"root"`
	LinkPath   string `json:"link_path"`
	TargetPath string `json:"target_path"`
}

type churnFact struct {
	Path   string `json:"path"`
	Streak int    `json:"streak"`
}

type sqliteFact struct {
	Root  string `json:"root"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

type onboardLayoutFacts struct {
	UnclassifiedDirs []dirStat     `json:"unclassified_dirs"`
	LargestDirs      []dirStat     `json:"largest_dirs"`
	ExternalSymlinks []symlinkFact `json:"external_symlinks"`
	ChurnFiles       []churnFact   `json:"churn_files"`
	UntrackedSQLite  []sqliteFact  `json:"untracked_sqlite"`
}

type onboardContextResult struct {
	Instance         onboardInstance       `json:"instance"`
	ProtectionPlan   statusPlan            `json:"protection_plan"`
	LastSeal         *statusSeal           `json:"last_seal,omitempty"`
	Protection       string                `json:"protection_summary"`
	EffectiveRules   onboardEffectiveRules `json:"effective_rules"`
	LayoutFacts      onboardLayoutFacts    `json:"layout_facts"`
	PendingProposals []agentkit.Proposal   `json:"pending_proposals"`
}

func (t *Tools) onboardContext(ctx context.Context) (any, error) {
	rec, prof, err := t.load()
	if err != nil {
		return nil, err
	}
	plan, _ := (porterprotection.Store{Home: t.Home}).LoadPlan(t.Instance)
	planSummary := summarizePlan(plan)
	lastSeal, protectionSummary := t.protectionSnapshot()
	ps, err := kit.LoadProposals(t.Home, t.Instance)
	if err != nil {
		return nil, err
	}
	t.audit("onboard_context", "ok", "", "")

	facts := t.collectLayoutFacts(ctx, rec, prof)
	var profSQLite []profile.SQLiteRef
	if prof.Consistency != nil {
		profSQLite = slices.Clone(prof.Consistency.SQLite)
	}
	profPaths := slices.Clone(prof.Paths)
	if profPaths == nil {
		profPaths = []profile.PathRule{}
	}
	ov := slices.Clone(rec.LocalOverrides)
	if ov == nil {
		ov = []profile.Override{}
	}
	return onboardContextResult{
		Instance: onboardInstance{
			InstanceID:     rec.InstanceID,
			ProfileID:      prof.ID,
			ProfileVersion: prof.ProfileVersion,
			ProfileStatus:  prof.Status,
			Roots:          rec.Roots,
		},
		ProtectionPlan: planSummary,
		LastSeal:       lastSeal,
		Protection:     protectionSummary,
		EffectiveRules: onboardEffectiveRules{
			ProfilePaths:   profPaths,
			ProfileSQLite:  profSQLite,
			LocalOverrides: ov,
			LocalSQLite:    slices.Clone(rec.LocalSQLite),
			ExternalRefs:   slices.Clone(rec.ExternalRefs),
		},
		LayoutFacts:      facts,
		PendingProposals: ps,
	}, nil
}

func (t *Tools) collectLayoutFacts(ctx context.Context, rec *enroll.Record, prof *profile.Profile) onboardLayoutFacts {
	roots := make([]enroll.Root, 0, len(rec.Roots))
	for id, path := range rec.Roots {
		st, err := os.Stat(path)
		r := enroll.Root{ID: id, Path: path, Exists: err == nil && st.IsDir()}
		if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
			r.Resolved = real
		}
		roots = append(roots, r)
	}
	sortRoots(roots)

	cls := profile.NewClassifier(prof, rec.LocalOverrides).WithSQLite(rec.LocalSQLite)
	var skip []string
	if t.Env != nil {
		if env := t.Env(ctx); env != nil {
			skip = porterenroll.NestedInstances(env.For(prof), prof, t.Instance, rec.Roots[profile.StateRoot])
		}
	}

	unclassMap := map[[2]string]*dirStat{}
	largestMap := map[[2]string]*dirStat{}
	extLinks := []symlinkFact{}
	untrackedDBs := []sqliteFact{}

	porterenroll.Walk(roots, skip, cls, func(it porterenroll.Item) {
		if it.Symlink {
			target, ok := externalSymlinkTarget(it.Abs, rec.Roots)
			if ok {
				extLinks = append(extLinks, symlinkFact{
					Root:       it.Root,
					LinkPath:   it.Root + ":" + it.Rel,
					TargetPath: target,
				})
			}
			return
		}
		if it.SidecarOf != "" {
			return
		}
		sz := it.Info.Size()
		lKey := [2]string{it.Root, layoutGroupPrefix(it.Rel)}
		ld := largestMap[lKey]
		if ld == nil {
			ld = &dirStat{Root: it.Root, Prefix: lKey[1]}
			largestMap[lKey] = ld
		}
		ld.Files++
		ld.Bytes += sz

		if it.Match.Category == "" {
			ud := unclassMap[lKey]
			if ud == nil {
				ud = &dirStat{Root: it.Root, Prefix: lKey[1]}
				unclassMap[lKey] = ud
			}
			ud.Files++
			ud.Bytes += sz
		}

		if it.SQLite == nil && sz >= 16 &&
			it.Match.Category != profile.CategoryBody &&
			it.Match.Category != profile.CategoryCache &&
			it.Match.Category != profile.CategoryIgnore {
			if isSQLiteHeader(it.Abs) {
				untrackedDBs = append(untrackedDBs, sqliteFact{
					Root:  it.Root,
					Path:  it.Rel,
					Bytes: sz,
				})
			}
		}
	}, func(string, string) {})

	churn := []churnFact{}
	for path, e := range seal.ReadSeen(t.paths(), t.Instance) {
		if e.Streak >= 2 {
			churn = append(churn, churnFact{Path: path, Streak: e.Streak})
		}
	}
	slices.SortFunc(churn, func(a, b churnFact) int {
		if c := cmp.Compare(b.Streak, a.Streak); c != 0 {
			return c
		}
		return cmp.Compare(a.Path, b.Path)
	})
	if len(churn) > onboardFactLimit {
		churn = churn[:onboardFactLimit]
	}

	slices.SortFunc(extLinks, func(a, b symlinkFact) int {
		return cmp.Compare(a.LinkPath, b.LinkPath)
	})
	if len(extLinks) > onboardFactLimit {
		extLinks = extLinks[:onboardFactLimit]
	}

	slices.SortFunc(untrackedDBs, func(a, b sqliteFact) int {
		if c := cmp.Compare(b.Bytes, a.Bytes); c != 0 {
			return c
		}
		return cmp.Compare(a.Root+":"+a.Path, b.Root+":"+b.Path)
	})
	if len(untrackedDBs) > onboardFactLimit {
		untrackedDBs = untrackedDBs[:onboardFactLimit]
	}

	return onboardLayoutFacts{
		UnclassifiedDirs: topDirStats(unclassMap, onboardFactLimit),
		LargestDirs:      topDirStats(largestMap, onboardFactLimit),
		ExternalSymlinks: extLinks,
		ChurnFiles:       churn,
		UntrackedSQLite:  untrackedDBs,
	}
}

func sortRoots(roots []enroll.Root) {
	slices.SortFunc(roots, func(a, b enroll.Root) int { return cmp.Compare(a.ID, b.ID) })
}

func layoutGroupPrefix(rel string) string {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) <= 2 {
		if len(parts) == 2 {
			return parts[0] + "/"
		}
		return rel
	}
	return parts[0] + "/" + parts[1] + "/"
}

func topDirStats(m map[[2]string]*dirStat, limit int) []dirStat {
	out := make([]dirStat, 0, len(m))
	for _, s := range m {
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b dirStat) int {
		if c := cmp.Compare(b.Bytes, a.Bytes); c != 0 {
			return c
		}
		return cmp.Compare(a.Root+":"+a.Prefix, b.Root+":"+b.Prefix)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func externalSymlinkTarget(linkAbs string, roots map[string]string) (string, bool) {
	rawTarget, err := os.Readlink(linkAbs)
	if err != nil {
		return "", false
	}
	resolved := rawTarget
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(linkAbs), resolved)
	}
	resolved = filepath.Clean(resolved)
	if eval, err := filepath.EvalSymlinks(linkAbs); err == nil {
		resolved = eval
	}
	if !filepath.IsAbs(resolved) || pathInsideRoots(resolved, roots) {
		return "", false
	}
	return resolved, true
}

func pathInsideRoots(target string, roots map[string]string) bool {
	realTarget, targetErr := filepath.EvalSymlinks(target)
	for _, root := range roots {
		if root == "" {
			continue
		}
		cleanRoot := filepath.Clean(root)
		if target == cleanRoot || strings.HasPrefix(target, cleanRoot+string(filepath.Separator)) {
			return true
		}
		if targetErr == nil {
			if realRoot, err := filepath.EvalSymlinks(cleanRoot); err == nil {
				if realTarget == realRoot || strings.HasPrefix(realTarget, realRoot+string(filepath.Separator)) {
					return true
				}
			}
		}
	}
	return false
}

// isSQLiteHeader reads only the 16-byte SQLite file header magic and never
// inspects database schema or table contents.
func isSQLiteHeader(absPath string) bool {
	fh, err := os.Open(absPath)
	if err != nil {
		return false
	}
	defer fh.Close()
	var hdr [16]byte
	if _, err := io.ReadFull(fh, hdr[:]); err != nil {
		return false
	}
	return string(hdr[:]) == sqliteMagic
}
