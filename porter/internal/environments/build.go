package environments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"runtime"
	"slices"
	"strings"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/vault"
	"valise/porter/internal/isolated"
)

var (
	ErrBuild      = errors.New("private environment materials or layout are invalid")
	ErrProgram    = errors.New("verified private program materials are missing or incompatible")
	ErrData       = errors.New("private environment data did not match its source manifest")
	ErrFoundation = errors.New("recorded-version data foundation is required; active data cannot be used")
)

// Layout contains authenticated source facts. Only roots within the source
// HOME can currently be reproduced. Outside roots require a future adapter.
type Layout struct {
	RuntimeExecutable string            `json:"runtime_executable,omitempty"`
	Home              string            `json:"home"`
	Roots             map[string]string `json:"roots"`
}

type ProgramFile struct {
	Path       string `json:"path"` // slash-separated, relative to private HOME
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Mode       uint32 `json:"mode"`
	LinkTarget string `json:"link_target,omitempty"`
}

// ProgramMaterial is produced by the trusted, pinned-source adapter, not by a
// remote request. A valid archive alone is never proof of official provenance.
// The builder accepts standard tar with exact regular-file/link manifests. It
// does not download, run installers, change shared programs or install dependencies.
type ProgramMaterial struct {
	SourceDigest string        `json:"source_digest"`
	ArchiveSHA   string        `json:"archive_sha256"`
	ArchiveBytes int64         `json:"archive_bytes"`
	OS           string        `json:"os"`
	Arch         string        `json:"arch"`
	Files        []ProgramFile `json:"files"`
}

type BuildInput struct {
	Runtime   *vault.RuntimeRecord
	Report    func(string)
	Profile   profile.Profile
	Overrides []profile.Override
	Layout    Layout
	Target    environment.Material
	Baseline  *environment.Material
	Program   ProgramMaterial
	Archive   io.Reader
	// OpenData reads only the exact authenticated snapshot/root/path requested.
	// It must use the vault source reader, never the active filesystem.
	OpenData func(context.Context, identity.SnapshotRef, string, string) (io.ReadCloser, error)
	MaxBytes int64
}

type Receipt struct {
	Source         environment.Source `json:"source"`
	ProgramSHA256  string             `json:"program_sha256"`
	LayoutDigest   string             `json:"layout_digest"`
	DataPlanDigest string             `json:"data_plan_digest"`
	Files          int                `json:"files"`
	Bytes          int64              `json:"bytes"`
	CandidateBuilt bool               `json:"candidate_built"`
}

type Candidate struct {
	Workspace *isolated.Workspace
	Spec      isolated.Spec
	Receipt   Receipt
}

// Digest uses the same canonical JSON representation as accepted profile
// identities. Layout identity also binds classification overrides.
func Digest(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func LayoutDigest(l Layout, overrides []profile.Override) string {
	return Digest(struct {
		Layout    Layout             `json:"layout"`
		Overrides []profile.Override `json:"overrides"`
	}{l, overrides})
}

// Build creates a fresh private candidate; it never stops or writes the live
// Agent. Errors leave incomplete private material under the owning operation's
// parent for explicit reconciliation/cleanup, never an activation receipt.
func Build(ctx context.Context, parent string, in BuildInput) (*Candidate, error) {
	if ctx == nil {
		return nil, ErrBuild
	}
	roots, programs, spec, plan, err := prepareBuild(in)
	if err != nil {
		return nil, err
	}
	report := func(stage string) {
		if in.Report != nil {
			in.Report(stage)
		}
	}
	report("layout_validated")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := isolated.NewWorkspaceForHome(parent, in.Layout.Home)
	if err != nil {
		return nil, err
	}
	if err := w.SetProgramPaths(programs); err != nil {
		return nil, ErrBuild
	}
	home, err := os.OpenRoot(w.HomeDir())
	if err != nil {
		return nil, err
	}
	defer home.Close()
	program, err := os.OpenRoot(w.ProgramDir())
	if err != nil {
		return nil, err
	}
	defer program.Close()
	for _, rel := range roots {
		if err := home.MkdirAll(rel, 0o700); err != nil {
			return nil, ErrBuild
		}
	}
	if err := unpackProgram(ctx, program, in.Program, in.Archive); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	report("program_verified")
	if err := writeData(ctx, home, roots, plan, in.OpenData); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	report("data_verified")
	// Resolve using the verified material graph. Absolute guest-HOME links
	// must never be followed as host paths while checking the entry.
	entry := strings.TrimPrefix(spec.Argv[0], in.Layout.Home+"/")
	graph, err := newProgramGraph(in.Program, programs, in.Layout.Home)
	if err != nil {
		return nil, err
	}
	resolved, directory, err := graph.resolve(entry)
	if err != nil || directory {
		return nil, &ProgramError{Code: "entry_graph"}
	}
	f, err := program.Open(resolved)
	if err != nil {
		return nil, &ProgramError{Code: "entry_missing"}
	}
	st, err := f.Stat()
	f.Close()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o100 == 0 || !inPrograms(entry, programs) {
		return nil, &ProgramError{Code: "entry_type_or_mode"}
	}
	report("entry_verified")
	if err := syncTree(w.HomeDir()); err != nil {
		return nil, err
	}
	if err := syncTree(w.ProgramDir()); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r := Receipt{Source: in.Target.Source, ProgramSHA256: in.Program.ArchiveSHA, LayoutDigest: in.Target.Source.LayoutDigest,
		DataPlanDigest: Digest(plan), CandidateBuilt: true, Files: len(in.Program.Files) + len(plan.Entries)}
	for _, f := range in.Program.Files {
		r.Bytes += f.Size
	}
	for _, e := range plan.Entries {
		if e.File != nil {
			r.Bytes += e.File.Size
		}
	}
	return &Candidate{w, spec, r}, nil
}

func prepareBuild(in BuildInput) (map[string]string, []string, isolated.Spec, environment.DataPlan, error) {
	bad := func(err error) (map[string]string, []string, isolated.Spec, environment.DataPlan, error) {
		return nil, nil, isolated.Spec{}, environment.DataPlan{}, err
	}
	if in.Archive == nil {
		return bad(ErrBuild)
	}
	roots, programs, spec, err := prepareProgram(in)
	if err != nil {
		return bad(err)
	}
	p, l := in.Profile, in.Layout
	ids := []string{profile.StateRoot}
	for _, r := range p.Roots {
		ids = append(ids, r.ID)
	}

	classifier := profile.NewClassifier(&p, in.Overrides)
	plan, err := environment.Compose(in.Target, in.Baseline, ids, func(root, rel string) string { return classifier.Classify(root, rel).Category })
	if err != nil {
		return bad(err)
	}
	if plan.NeedsDataFoundation {
		return bad(ErrFoundation)
	}
	if err := validateDestinations(in.Program, plan, roots, programs, l.Home, in.MaxBytes); err != nil {
		return bad(err)
	}
	if len(plan.Entries) != 0 && in.OpenData == nil {
		return bad(ErrData)
	}
	return roots, programs, spec, plan, nil
}

func prepareProgram(in BuildInput) (map[string]string, []string, isolated.Spec, error) {
	bad := func(err error) (map[string]string, []string, isolated.Spec, error) {
		return nil, nil, isolated.Spec{}, err
	}
	p, l := in.Profile, in.Layout
	if p.Environments == nil || p.Environments.Engine != "linux-bwrap" || in.MaxBytes <= 0 ||
		in.Target.Source.ProfileDigest != Digest(p) || in.Target.Source.LayoutDigest != LayoutDigest(l, in.Overrides) {
		return bad(ErrBuild)
	}
	if results := profile.Lint([]*profile.Document{{Profile: &p}}); len(results) != 1 || len(results[0].Issues) != 0 {
		return bad(ErrBuild)
	}
	if in.Program.SourceDigest != in.Target.Source.ProgramDigest || in.Program.OS != runtime.GOOS || in.Program.Arch != runtime.GOARCH ||
		!validSHA(in.Program.ArchiveSHA) || in.Program.ArchiveBytes <= 0 || len(in.Program.Files) == 0 {
		return bad(ErrProgram)
	}
	if !isolated.ValidHome(l.Home) {
		return bad(ErrBuild)
	}
	roots := map[string]string{}
	seenRoots := map[string]bool{}
	ids := []string{profile.StateRoot}
	for _, r := range p.Roots {
		ids = append(ids, r.ID)
	}
	if len(l.Roots) != len(ids) {
		return bad(ErrBuild)
	}
	for _, id := range ids {
		v := l.Roots[id]
		if seenRoots[v] || !strings.HasPrefix(v, l.Home+"/") || !safeRelative(strings.TrimPrefix(v, l.Home+"/")) {
			return bad(ErrBuild)
		}
		seenRoots[v] = true
		roots[id] = strings.TrimPrefix(v, l.Home+"/")
	}
	var programs []string
	for _, ref := range p.Environments.ProgramPaths {
		programs = append(programs, path.Join(roots[ref.Root], ref.Glob))
	}
	if p.Environments.RuntimeExecutable != nil {
		if !strings.HasPrefix(l.RuntimeExecutable, l.Home+"/") || !safeRelative(strings.TrimPrefix(l.RuntimeExecutable, l.Home+"/")) {
			return bad(ErrProgram)
		}
		rel := strings.TrimPrefix(l.RuntimeExecutable, l.Home+"/")
		category := sourceCategory(InstalledSource{Profile: p, Overrides: in.Overrides, Layout: l}, rel)
		if path.Base(rel) != p.Environments.RuntimeExecutable.Name || category == profile.CategorySecrets || !inPrograms(rel, programs) && category != profile.CategoryBody {
			return bad(ErrProgram)
		}
		if !inPrograms(rel, programs) {
			programs = append(programs, path.Dir(rel))
		}
	} else if l.RuntimeExecutable != "" {
		return bad(ErrProgram)
	}
	for i, a := range programs {
		for _, root := range roots {
			if root == a || strings.HasPrefix(root, a+"/") {
				return bad(ErrProgram)
			}
		}
		for _, b := range programs[:i] {
			if a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
				return bad(ErrProgram)
			}
		}
	}
	spec := isolated.Spec{Home: l.Home, WorkingDir: expandPrivate(p.Environments.WorkingDir, l.Home, roots[profile.StateRoot], l.RuntimeExecutable)}
	for _, a := range p.Environments.StartArgv {
		spec.Argv = append(spec.Argv, expandPrivate(a, l.Home, roots[profile.StateRoot], l.RuntimeExecutable))
	}
	for _, d := range p.Environments.Path {
		spec.Path = append(spec.Path, expandPrivate(d, l.Home, roots[profile.StateRoot], l.RuntimeExecutable))
	}
	if len(spec.Argv) == 0 || !strings.HasPrefix(spec.Argv[0], l.Home+"/") || !inPrograms(strings.TrimPrefix(spec.Argv[0], l.Home+"/"), programs) {
		return bad(ErrProgram)
	}
	for _, o := range in.Overrides {
		if o.Validate(&p) != nil {
			return bad(ErrBuild)
		}
	}
	classifier := profile.NewClassifier(&p, in.Overrides)
	for _, file := range in.Program.Files {
		for id, root := range roots {
			if strings.HasPrefix(file.Path, root+"/") && classifier.Classify(id, strings.TrimPrefix(file.Path, root+"/")).Category == profile.CategorySecrets {
				return bad(ErrProgram)
			}
		}
	}
	if err := validateDestinations(in.Program, environment.DataPlan{}, roots, programs, l.Home, in.MaxBytes); err != nil {
		return bad(err)
	}
	return roots, programs, spec, nil
}

func expandPrivate(s, home, state, runtime string) string {
	dir := ""
	if runtime != "" {
		dir = path.Dir(runtime)
	}
	return strings.NewReplacer("${HOME}", home, "${STATE_DIR}", path.Join(home, state), "${RUNTIME_EXECUTABLE}", runtime, "${RUNTIME_DIR}", dir).Replace(s)
}

func safeRelative(p string) bool {
	return p != "" && p != "." && p != ".." && path.Clean(p) == p && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\\\x00\r\n")
}

func inPrograms(p string, dirs []string) bool {
	return slices.ContainsFunc(dirs, func(dir string) bool { return strings.HasPrefix(p, dir+"/") })
}

func validSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && strings.ToLower(s) == s
}
