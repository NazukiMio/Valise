package environments

import (
	"path"
	"strings"

	"valise/porter/internal/isolated"
)

// Command resolves only a fixed accepted-profile command inside a verified
// programme graph. It never consults PATH on the host or expands user input.
func Command(b *ProgramBinding, argv []string) (isolated.Spec, error) {
	if b == nil || len(argv) == 0 || len(argv) > 128 {
		return isolated.Spec{}, ErrProgram
	}
	s := b.Spec
	s.Argv = make([]string, len(argv))
	vars := strings.NewReplacer("${HOME}", b.Layout.Home, "${STATE_DIR}", b.Layout.Roots["state"],
		"${RUNTIME_EXECUTABLE}", b.Layout.RuntimeExecutable, "${RUNTIME_DIR}", path.Dir(b.Layout.RuntimeExecutable))
	for i, arg := range argv {
		s.Argv[i] = vars.Replace(arg)
		if len(arg) > 16384 || strings.ContainsAny(s.Argv[i], "\x00\r\n") || strings.Contains(s.Argv[i], "${") {
			return isolated.Spec{}, ErrProgram
		}
	}
	g, err := newProgramGraph(b.Program, b.Paths, b.Layout.Home)
	if err != nil {
		return isolated.Spec{}, err
	}
	candidates := []string{s.Argv[0]}
	if !path.IsAbs(s.Argv[0]) {
		if strings.Contains(s.Argv[0], "/") {
			return isolated.Spec{}, ErrProgram
		}
		candidates = nil
		for _, dir := range s.Path {
			candidates = append(candidates, path.Join(dir, s.Argv[0]))
		}
	}
	for _, guest := range candidates {
		if !strings.HasPrefix(guest, b.Layout.Home+"/") || path.Clean(guest) != guest {
			continue
		}
		rel := strings.TrimPrefix(guest, b.Layout.Home+"/")
		target, directory, err := g.resolve(rel)
		f, ok := g.nodes[target]
		if err == nil && !directory && ok && f.LinkTarget == "" && f.Mode&0100 != 0 {
			s.Argv[0] = guest
			return s, nil
		}
	}
	return isolated.Spec{}, ErrProgram
}
