package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Argv is a command written as an argv array. Shell strings are rejected at
// parse time.
type Argv []string

// UnmarshalYAML accepts null or a sequence of scalars.
func (a *Argv) UnmarshalYAML(n *yaml.Node) error {
	switch {
	case n.Kind == yaml.ScalarNode && n.Tag == "!!null":
		*a = nil
		return nil
	case n.Kind == yaml.ScalarNode:
		return fmt.Errorf("line %d: 命令必须写成 argv 数组，不允许 shell 字符串 %q", n.Line, n.Value)
	case n.Kind != yaml.SequenceNode:
		return fmt.Errorf("line %d: 命令必须写成 argv 数组", n.Line)
	}
	out := make(Argv, 0, len(n.Content))
	for _, c := range n.Content {
		if c.Kind != yaml.ScalarNode || c.Tag == "!!null" {
			return fmt.Errorf("line %d: argv 的每一项都必须是字符串", c.Line)
		}
		out = append(out, c.Value)
	}
	*a = out
	return nil
}

// PendingMarker is the text that marks an unverified entry.
const PendingMarker = "verify: pending"

// Pending is one unverified marker found in a profile file.
type Pending struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Document is a parsed profile file.
type Document struct {
	Path    string
	Profile *Profile
	// TopKeys are the top-level keys present in the file, used to enforce
	// what a child profile may declare.
	TopKeys []string
	// Pending lists every `verify: pending` field and comment.
	Pending []Pending
}

// LoadFile parses a profile file.
func LoadFile(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	d.Path = path
	return d, nil
}

// Parse parses one profile document strictly: unknown fields are errors.
func Parse(data []byte) (*Document, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var p Profile
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("文件为空")
		}
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("一个文件只能包含一份 profile")
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	d := &Document{Profile: &p}
	if len(root.Content) == 1 && root.Content[0].Kind == yaml.MappingNode {
		m := root.Content[0]
		for i := 0; i+1 < len(m.Content); i += 2 {
			d.TopKeys = append(d.TopKeys, m.Content[i].Value)
		}
	} else {
		return nil, errors.New("profile 顶层必须是映射")
	}
	collectPending(&root, &d.Pending)
	return d, nil
}

func collectPending(n *yaml.Node, out *[]Pending) {
	for _, c := range []string{n.HeadComment, n.LineComment, n.FootComment} {
		for _, line := range strings.Split(c, "\n") {
			if strings.Contains(line, PendingMarker) {
				*out = append(*out, Pending{Line: n.Line, Text: strings.TrimSpace(line)})
			}
		}
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value == "verify" && v.Kind == yaml.ScalarNode && v.Value == "pending" {
				*out = append(*out, Pending{Line: k.Line, Text: PendingMarker})
			}
		}
	}
	for _, c := range n.Content {
		collectPending(c, out)
	}
}
