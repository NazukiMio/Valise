package environments

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"slices"
	"strings"
)

// programGraph resolves guest links without following them on the host. Every
// target is backed by the same frozen material, never by data or a host binary.
type programGraph struct {
	home        string
	dirs        []string
	nodes       map[string]ProgramFile
	directories map[string]bool
}

func newProgramGraph(material ProgramMaterial, dirs []string, home string) (*programGraph, error) {
	g := &programGraph{home: home, dirs: dirs, nodes: map[string]ProgramFile{}, directories: map[string]bool{}}
	for _, dir := range dirs {
		g.directories[dir] = true
		for parent := path.Dir(dir); parent != "."; parent = path.Dir(parent) {
			g.directories[parent] = true
		}
	}
	for _, file := range material.Files {
		g.nodes[file.Path] = file
		for parent := path.Dir(file.Path); parent != "."; parent = path.Dir(parent) {
			g.directories[parent] = true
		}
	}
	edges := map[string][]string{}
	for dir := range g.directories {
		if parent := path.Dir(dir); g.contains(dir) && g.contains(parent) {
			edges[parent] = append(edges[parent], dir)
		}
	}
	for _, file := range material.Files {
		if file.LinkTarget == "" {
			continue
		}
		h := sha256.Sum256([]byte(file.LinkTarget))
		if file.Size != 0 || file.Mode != 0o777 || file.SHA256 != hex.EncodeToString(h[:]) {
			return nil, ErrProgram
		}
		target, directory, err := g.resolve(file.Path)
		if err != nil || directory && strings.HasPrefix(file.Path, target+"/") {
			return nil, ErrProgram
		}
		if directory {
			edges[path.Dir(file.Path)] = append(edges[path.Dir(file.Path)], target)
		}
	}
	if directoryCycle(edges) {
		return nil, ErrProgram
	}
	return g, nil
}

func directoryCycle(edges map[string][]string) bool {
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(dir string) bool {
		if state[dir] != 0 {
			return state[dir] == 1
		}
		state[dir] = 1
		for _, target := range edges[dir] {
			if visit(target) {
				return true
			}
		}
		state[dir] = 2
		return false
	}
	for dir := range edges {
		if visit(dir) {
			return true
		}
	}
	return false
}

func (g *programGraph) target(file ProgramFile) (string, error) {
	target := file.LinkTarget
	if target == "" || len(target) > 4096 || strings.ContainsAny(target, "\\\x00\r\n") {
		return "", ErrProgram
	}
	if path.IsAbs(target) {
		if path.Clean(target) != target || !strings.HasPrefix(target, g.home+"/") {
			return "", ErrProgram
		}
		target = strings.TrimPrefix(target, g.home+"/")
	} else {
		target = path.Join(path.Dir(file.Path), target)
	}
	if !safeRelative(target) || !g.contains(target) {
		return "", ErrProgram
	}
	return target, nil
}

func (g *programGraph) contains(p string) bool {
	return inPrograms(p, g.dirs) || slices.Contains(g.dirs, p)
}

func (g *programGraph) resolve(p string) (string, bool, error) {
	visited := map[string]bool{}
	for len(visited) <= 64 {
		if !g.contains(p) {
			return "", false, ErrProgram
		}
		parts := strings.Split(p, "/")
		restart := false
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			file, ok := g.nodes[prefix]
			if !ok || file.LinkTarget == "" {
				continue
			}
			if visited[prefix] {
				return "", false, ErrProgram
			}
			visited[prefix] = true
			target, err := g.target(file)
			if err != nil {
				return "", false, err
			}
			p = path.Join(append([]string{target}, parts[i+1:]...)...)
			restart = true
			break
		}
		if restart {
			continue
		}
		if _, file := g.nodes[p]; file {
			return p, false, nil
		}
		if g.directories[p] {
			return p, true, nil
		}
		return "", false, ErrProgram
	}
	return "", false, ErrProgram
}
