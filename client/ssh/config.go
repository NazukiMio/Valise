// Package ssh drives the system OpenSSH client: ~/.ssh/config parsing for
// display and import, one-shot remote commands, ControlMaster reuse and the
// stdio JSON-RPC client. Valise never handles SSH passwords or private keys.
package ssh

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// HostConfig is the subset of ssh_config Valise shows and relies on.
type HostConfig struct {
	Alias         string   `json:"alias"`
	HostName      string   `json:"host_name"`
	User          string   `json:"user,omitempty"`
	Port          int      `json:"port,omitempty"`
	ProxyJump     string   `json:"proxy_jump,omitempty"`
	IdentityFiles []string `json:"identity_files,omitempty"`
	IdentityAgent string   `json:"identity_agent,omitempty"`
	ControlMaster string   `json:"control_master,omitempty"`
	ControlPath   string   `json:"control_path,omitempty"`
}

// UserControlMaster reports whether the user configured connection sharing
// for this host, in which case Valise reuses it instead of adding its own.
func (h HostConfig) UserControlMaster() bool {
	return h.ControlPath != "" && !strings.EqualFold(h.ControlPath, "none")
}

type directive struct {
	key  string // lower case
	args []string
}

type block struct {
	patterns []string // nil for the implicit top block
	match    bool     // Match blocks are never applied
	dirs     []directive
}

// Config is a parsed ssh_config.
type Config struct {
	blocks []*block
	home   string
}

// maxIncludeDepth mirrors OpenSSH's limit on nested Include.
const maxIncludeDepth = 16

// ParseFile parses an ssh_config file and its Include directives. A missing
// file yields an empty config. home expands "~" and resolves relative
// Include paths (against home/.ssh).
func ParseFile(file, home string) (*Config, error) {
	c := &Config{home: home, blocks: []*block{{}}}
	if err := c.parseFile(file, 0); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return c, nil
}

// Parse parses ssh_config text.
func Parse(r io.Reader, home string) (*Config, error) {
	c := &Config{home: home, blocks: []*block{{}}}
	return c, c.parse(r, 0)
}

func (c *Config) parseFile(file string, depth int) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	return c.parse(f, depth)
}

func (c *Config) parse(r io.Reader, depth int) error {
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		key, args, err := splitLine(sc.Text())
		if err != nil {
			return fmt.Errorf("ssh_config 第 %d 行：%w", n, err)
		}
		switch key {
		case "":
		case "host":
			c.blocks = append(c.blocks, &block{patterns: args})
		case "match":
			c.blocks = append(c.blocks, &block{match: true})
		case "include":
			if depth >= maxIncludeDepth {
				return errors.New("Include 嵌套过深")
			}
			cur := c.blocks[len(c.blocks)-1]
			for _, pat := range args {
				if err := c.include(pat, depth+1); err != nil {
					return err
				}
			}
			// Host lines inside an included file do not leak out: lines
			// after the Include belong to the including block again.
			if c.blocks[len(c.blocks)-1] != cur {
				c.blocks = append(c.blocks, &block{patterns: cur.patterns, match: cur.match})
			}
		default:
			cur := c.blocks[len(c.blocks)-1]
			cur.dirs = append(cur.dirs, directive{key, args})
		}
	}
	return sc.Err()
}

func (c *Config) include(pat string, depth int) error {
	pat = c.expandTilde(pat)
	if !filepath.IsAbs(pat) {
		pat = filepath.Join(c.home, ".ssh", pat)
	}
	files, err := filepath.Glob(pat)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := c.parseFile(f, depth); err != nil {
			return err
		}
	}
	return nil
}

// splitLine returns the lower-case keyword and its arguments. It accepts
// "Key value", "Key=value" and "Key = value", with double-quoted arguments.
func splitLine(line string) (string, []string, error) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", nil, nil
	}
	end := strings.IndexAny(line, " \t=")
	if end < 0 {
		return strings.ToLower(line), nil, nil
	}
	key := strings.ToLower(line[:end])
	rest := strings.TrimLeft(line[end:], " \t")
	if strings.HasPrefix(rest, "=") {
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	var args []string
	for rest != "" {
		if rest[0] == '"' {
			q := strings.IndexByte(rest[1:], '"')
			if q < 0 {
				return "", nil, errors.New("引号不匹配")
			}
			args = append(args, rest[1:1+q])
			rest = rest[2+q:]
		} else {
			e := strings.IndexAny(rest, " \t")
			if e < 0 {
				e = len(rest)
			}
			args = append(args, rest[:e])
			rest = rest[e:]
		}
		rest = strings.TrimLeft(rest, " \t")
	}
	return key, args, nil
}

func (c *Config) expandTilde(p string) string {
	if p == "~" {
		return c.home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(c.home, p[2:])
	}
	return p
}

// matches implements Host pattern lists: any positive match and no negated
// match.
func (b *block) matches(alias string) bool {
	if b.match {
		return false
	}
	if b.patterns == nil {
		return true
	}
	hit := false
	for _, p := range b.patterns {
		neg := strings.HasPrefix(p, "!")
		ok, _ := path.Match(strings.ToLower(strings.TrimPrefix(p, "!")), strings.ToLower(alias))
		if ok && neg {
			return false
		}
		hit = hit || ok
	}
	return hit
}

// Lookup resolves the effective settings for alias. As in OpenSSH, the first
// value obtained for each keyword wins, except IdentityFile, which
// accumulates. Match blocks are not evaluated.
func (c *Config) Lookup(alias string) HostConfig {
	h := HostConfig{Alias: alias}
	for _, b := range c.blocks {
		if !b.matches(alias) {
			continue
		}
		for _, d := range b.dirs {
			if len(d.args) == 0 {
				continue
			}
			v := d.args[0]
			switch d.key {
			case "hostname":
				if h.HostName == "" {
					h.HostName = strings.ReplaceAll(strings.ReplaceAll(v, "%h", alias), "%%", "%")
				}
			case "user":
				if h.User == "" {
					h.User = v
				}
			case "port":
				if p, err := strconv.Atoi(v); err == nil && h.Port == 0 {
					h.Port = p
				}
			case "proxyjump":
				if h.ProxyJump == "" {
					h.ProxyJump = v
				}
			case "identityfile":
				h.IdentityFiles = append(h.IdentityFiles, c.expandTilde(v))
			case "identityagent":
				if h.IdentityAgent == "" {
					h.IdentityAgent = c.expandTilde(v)
				}
			case "controlmaster":
				if h.ControlMaster == "" {
					h.ControlMaster = v
				}
			case "controlpath":
				if h.ControlPath == "" {
					h.ControlPath = v
				}
			}
		}
	}
	if h.HostName == "" {
		h.HostName = alias
	}
	return h
}

// Hosts returns the concrete aliases declared in Host lines, without
// wildcard or negated patterns, in file order.
func (c *Config) Hosts() []string {
	var out []string
	seen := map[string]bool{}
	for _, b := range c.blocks {
		for _, p := range b.patterns {
			if strings.ContainsAny(p, "*?!") || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
