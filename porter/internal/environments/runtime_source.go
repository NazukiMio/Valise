package environments

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"valise/core/vault"
)

type runtimeSource struct {
	File       ProgramFile
	Dependency vault.RuntimeDependency
	PID        int
	Start      string
}

func declaredRuntime(in InstalledSource) bool {
	if in.Profile.Environments == nil || in.Profile.Environments.RuntimeExecutable == nil {
		return false
	}
	r := in.Profile.Environments.RuntimeExecutable
	return r.Service.Scope == "user" && in.Profile.Service != nil && slices.Contains(in.Profile.Service.Candidates, r.Service) && r.Name != "" && filepath.Base(r.Name) == r.Name && !strings.ContainsAny(r.Name, "\\\x00\r\n")
}

func addObservedRuntime(ctx context.Context, in InstalledSource, dirs []string, files []ProgramFile) ([]string, []ProgramFile, *runtimeSource, error) {
	if in.Profile.Environments == nil || in.Profile.Environments.RuntimeExecutable == nil {
		return dirs, files, nil, nil
	}
	if !declaredRuntime(in) {
		return nil, nil, nil, ErrSource
	}
	r, err := observeRuntimeSource(ctx, in)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, f := range files {
		if f.Path == r.File.Path {
			if f != r.File {
				return nil, nil, nil, ErrSource
			}
			return dirs, files, r, nil
		}
	}
	var size int64
	for _, f := range files {
		size += f.Size
	}
	if r.File.Size > in.MaxBytes-size {
		return nil, nil, nil, ErrSource
	}
	if !inPrograms(r.File.Path, dirs) {
		dir := path.Dir(r.File.Path)
		if dir == "." {
			return nil, nil, nil, ErrSource
		}
		for _, other := range dirs {
			if strings.HasPrefix(other, dir+"/") {
				return nil, nil, nil, ErrSource
			}
		}
		dirs = append(append([]string{}, dirs...), dir)
	}
	return dirs, append(files, r.File), r, nil
}
