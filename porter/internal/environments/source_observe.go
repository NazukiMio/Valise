package environments

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/vault"
)

var ErrSource = errors.New("registered program source or complete private runtime evidence is unavailable or changed")

// InstalledSource is supplied by the authenticated snapshot/registration
// controller. It is not an RPC request or a filesystem-copy interface.
type InstalledSource struct {
	CheckSpace func(ProgramMaterial) error
	Profile    profile.Profile
	Overrides  []profile.Override
	Layout     Layout
	Origin     enroll.Provenance
	MaxBytes   int64
}

type ProgramObservation struct {
	RuntimeExecutable string
	Material          ProgramMaterial
	Dependencies      []vault.RuntimeDependency
	directories       []string
	runtime           *runtimeSource
}

// ObserveInstalledNPM only reads profile-declared program files and public
// package metadata. It neither consults PATH nor executes npm, an interpreter,
// the harness or an installer. Every dependency is observed or none is returned.
func ObserveInstalledNPM(ctx context.Context, in InstalledSource) (*ProgramObservation, error) {
	if ctx == nil || in.MaxBytes <= 0 || !filepath.IsAbs(in.Layout.Home) || filepath.Clean(in.Layout.Home) != in.Layout.Home {
		return nil, ErrSource
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dirs, err := sourceDirectories(in)
	if err != nil {
		return nil, err
	}
	files, err := scanProgramTrees(ctx, in, dirs)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	bodyFiles := append([]ProgramFile{}, files...)
	programs, files, boundRuntime, err := addObservedRuntime(ctx, in, dirs, files)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	material := ProgramMaterial{OS: runtime.GOOS, Arch: runtime.GOARCH, Files: files}
	if _, err := newProgramGraph(material, programs, filepath.ToSlash(in.Layout.Home)); err != nil {
		return nil, ErrSource
	}
	if err := checkInstalledNPM(in, dirs); err != nil {
		return nil, err
	}
	again, err := scanProgramTrees(ctx, in, dirs)
	if err != nil || Digest(bodyFiles) != Digest(again) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrSource
	}
	dependencies := treeDependencies(in, dirs, files)
	executable := ""
	if boundRuntime != nil {
		check, err := observeRuntimeSource(ctx, in)
		if err != nil || check.PID != boundRuntime.PID || check.Start != boundRuntime.Start || check.File != boundRuntime.File {
			return nil, ErrSource
		}
		executable = boundRuntime.Dependency.Path
		if in.Layout.RuntimeExecutable != "" && in.Layout.RuntimeExecutable != executable {
			return nil, ErrSource
		}
		dependencies = append(dependencies, boundRuntime.Dependency)
	}
	material.SourceDigest = ProgramSourceDigest(in.Origin)
	return &ProgramObservation{Material: material, Dependencies: dependencies, directories: programs, RuntimeExecutable: executable, runtime: boundRuntime}, nil
}

func sourceDirectories(in InstalledSource) ([]string, error) {
	if in.Profile.Body == nil || len(in.Profile.Body.Paths) == 0 {
		return nil, ErrSource
	}
	var dirs []string
	for _, ref := range in.Profile.Body.Paths {
		rel, ok := strings.CutSuffix(ref.Glob, "/**")
		root := in.Layout.Roots[ref.Root]
		if !ok || !profile.IsLiteral(rel) || !safeRelative(rel) || !strings.HasPrefix(root, in.Layout.Home+string(filepath.Separator)) || filepath.Clean(root) != root {
			return nil, ErrSource
		}
		dir := path.Join(filepath.ToSlash(strings.TrimPrefix(root, in.Layout.Home+string(filepath.Separator))), rel)
		if !safeRelative(dir) {
			return nil, ErrSource
		}
		for _, other := range dirs {
			if dir == other || strings.HasPrefix(dir, other+"/") || strings.HasPrefix(other, dir+"/") {
				return nil, ErrSource
			}
		}
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs, nil
}

func checkInstalledNPM(in InstalledSource, dirs []string) error {
	p, origin := in.Profile.Provenance, in.Origin
	if p == nil || p.Kind != profile.ProvenanceNPMGlobal || p.NPM == nil || origin.Kind != p.Kind || origin.NPM == nil || origin.Git != nil || origin.Binary != nil || origin.Tool != nil {
		return ErrSource
	}
	n := origin.NPM
	if n.Package != p.NPM.Package || n.Package == "" || n.Version == "" || !validIntegrity(n.Integrity) || !filepath.IsAbs(n.Prefix) || filepath.Clean(n.Prefix) != n.Prefix {
		return ErrSource
	}
	registry, err := url.Parse(n.Registry)
	if err != nil || registry.Scheme != "https" || registry.Hostname() == "" || registry.User != nil || registry.RawQuery != "" || registry.ForceQuery || registry.Fragment != "" {
		return ErrSource
	}
	if !safeRelative(n.Package) {
		return ErrSource
	}
	pkg := filepath.Join(n.Prefix, "lib", "node_modules", filepath.FromSlash(n.Package), "package.json")
	if !strings.HasPrefix(pkg, in.Layout.Home+string(filepath.Separator)) {
		return ErrSource
	}
	rel := filepath.ToSlash(strings.TrimPrefix(pkg, in.Layout.Home+string(filepath.Separator)))
	if !inPrograms(rel, dirs) || sourceCategory(in, rel) != profile.CategoryBody {
		return ErrSource
	}
	f, err := openSourceFile(in, dirs, rel)
	if err != nil {
		return ErrSource
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 8<<20 {
		return ErrSource
	}
	var metadata struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	d := json.NewDecoder(io.LimitReader(f, 8<<20+1))
	if d.Decode(&metadata) != nil || metadata.Name != n.Package || metadata.Version != n.Version {
		return ErrSource
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrSource
	}
	return nil
}

func validIntegrity(s string) bool {
	if len(s) > 256 || strings.ContainsAny(s, "\x00\r\n") {
		return false
	}
	algorithm, encoded, ok := strings.Cut(s, "-")
	data, err := base64.StdEncoding.DecodeString(encoded)
	return ok && err == nil && (algorithm == "sha512" && len(data) == 64 || algorithm == "sha256" && len(data) == 32)
}

func sourceCategory(in InstalledSource, rel string) string {
	for id, root := range in.Layout.Roots {
		prefix := filepath.ToSlash(strings.TrimPrefix(root, in.Layout.Home+string(filepath.Separator))) + "/"
		if strings.HasPrefix(rel, prefix) {
			if category := profile.NewClassifier(&in.Profile, in.Overrides).Classify(id, strings.TrimPrefix(rel, prefix)).Category; category != profile.CategoryBody {
				return category
			}
		}
	}
	return profile.CategoryBody
}

// ProgramSourceDigest binds the accepted exact source/version. The program
// tree and archive digests separately bind concrete observed/prepared bytes.
func ProgramSourceDigest(origin enroll.Provenance) string { return Digest(origin) }
