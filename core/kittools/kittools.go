// Package kittools is the Valise Kit tool list (docs/AGENT_KIT.md 8.4.1):
// reviewed tools a user may choose to install, shipped in the signed
// profile pack as kit-tools.yaml. It is a list of fixed entries, not an
// interpreter.
package kittools

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Schema is the list's schema line.
const Schema = "valise-kit-tools/1"

// Tiers.
const (
	TierEnhanced = "enhanced" // needs the Valise passphrase
	TierExpert   = "expert"   // needs advanced mode
)

// List is kit-tools.yaml.
type List struct {
	Schema string `yaml:"schema" json:"schema"`
	Tools  []Tool `yaml:"tools" json:"tools"`
}

// Tool is one reviewed tool.
type Tool struct {
	ID        string    `yaml:"id" json:"id"`
	Tier      string    `yaml:"tier" json:"tier"`
	Name      string    `yaml:"name" json:"name"`
	Purpose   string    `yaml:"purpose" json:"purpose"`
	Without   string    `yaml:"without" json:"without"`
	Check     Check     `yaml:"check" json:"check"`
	Binary    *Binary   `yaml:"binary" json:"binary,omitempty"`
	Install   []Command `yaml:"install" json:"install,omitempty"`
	Uninstall []Command `yaml:"uninstall" json:"uninstall,omitempty"`
	Verify    string    `yaml:"verify" json:"verify,omitempty"`
}

// Check is how porter tells whether the tool is there.
type Check struct {
	Binary string `yaml:"binary" json:"binary,omitempty"`
	Linger bool   `yaml:"linger" json:"linger,omitempty"`
}

// Binary is an official build put into ~/.local/bin/<check.binary>.
type Binary struct {
	Version  string    `yaml:"version" json:"version"`
	Archives []Archive `yaml:"archives" json:"archives"`
}

// Archive is one architecture's download.
type Archive struct {
	Arch   string `yaml:"arch" json:"arch"`
	URL    string `yaml:"url" json:"url"`
	SHA256 string `yaml:"sha256" json:"sha256"`
	Member string `yaml:"member" json:"member"`
}

// Command runs on the listed distributions (os-release ID).
type Command struct {
	OSID []string `yaml:"os_id" json:"os_id"`
	Argv []string `yaml:"argv" json:"argv"`
}

// Pending reports a tool that may be shown but not installed.
func (t Tool) Pending() bool {
	if t.Verify == "pending" {
		return true
	}
	if t.Binary != nil {
		if t.Binary.Version == "" || len(t.Binary.Archives) == 0 {
			return true
		}
		for _, a := range t.Binary.Archives {
			if a.SHA256 == "" {
				return true
			}
		}
	}
	return false
}

// For returns the command for an os-release ID.
func For(cmds []Command, osID string) []string {
	for _, c := range cmds {
		for _, id := range c.OSID {
			if id == osID {
				return c.Argv
			}
		}
	}
	return nil
}

// Archive returns the download for an architecture.
func (b *Binary) Archive(arch string) *Archive {
	for i := range b.Archives {
		if b.Archives[i].Arch == arch {
			return &b.Archives[i]
		}
	}
	return nil
}

// ByID finds a tool.
func (l *List) ByID(id string) *Tool {
	for i := range l.Tools {
		if l.Tools[i].ID == id {
			return &l.Tools[i]
		}
	}
	return nil
}

var (
	idRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
	shaRe    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	varRe    = regexp.MustCompile(`\$\{[^}]*\}`)
	memberRe = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)
)

// Parse reads kit-tools.yaml strictly and checks it.
func Parse(data []byte) (*List, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var l List
	if err := dec.Decode(&l); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("文件为空")
		}
		return nil, err
	}
	if err := l.Lint(); err != nil {
		return nil, err
	}
	return &l, nil
}

// Lint checks the rules of docs/AGENT_KIT.md 8.4.1.
func (l *List) Lint() error {
	var errs []string
	bad := func(id, format string, a ...any) { errs = append(errs, id+"："+fmt.Sprintf(format, a...)) }
	if l.Schema != Schema {
		errs = append(errs, "schema 必须是 "+Schema)
	}
	seen := map[string]bool{}
	for _, t := range l.Tools {
		id := t.ID
		if !idRe.MatchString(id) || seen[id] {
			bad(id, "id 无效或重复")
		}
		seen[id] = true
		if t.Name == "" || t.Purpose == "" || t.Without == "" {
			bad(id, "name、purpose、without 都要写")
		}
		if (t.Check.Binary == "") == !t.Check.Linger {
			bad(id, "check 要写 binary 或 linger 中的一个")
		}
		if t.Check.Binary != "" && !idRe.MatchString(t.Check.Binary) {
			bad(id, "check.binary 只能是程序名")
		}
		if t.Verify != "" && t.Verify != "pending" {
			bad(id, "verify 只能是 pending 或不写")
		}
		switch t.Tier {
		case TierEnhanced:
			if t.Binary == nil || len(t.Install) > 0 || t.Check.Binary == "" {
				bad(id, "增强级只能是官方二进制（binary 与 check.binary），不能运行命令")
				continue
			}
			if t.Verify != "pending" && t.Binary.Version == "" {
				bad(id, "已核实的条目要锁定版本")
			}
			for _, a := range t.Binary.Archives {
				if t.Binary.Version != "" && !strings.Contains(a.URL, t.Binary.Version) {
					bad(id, "下载地址要包含锁定的版本号：%s", a.URL)
				}
				u, err := url.Parse(a.URL)
				if err != nil || u.Scheme != "https" || u.Host == "" {
					bad(id, "下载地址必须是 https：%s", a.URL)
				}
				if a.SHA256 != "" && !shaRe.MatchString(a.SHA256) {
					bad(id, "sha256 格式不对")
				}
				if a.Arch != "amd64" && a.Arch != "arm64" {
					bad(id, "arch 只能是 amd64 或 arm64")
				}
				if !memberRe.MatchString(a.Member) || strings.Contains(a.Member, "..") {
					bad(id, "member 路径无效")
				}
			}
		case TierExpert:
			if t.Binary != nil || len(t.Install) == 0 {
				bad(id, "专家级只能写 install 命令")
			}
		default:
			bad(id, "tier 只能是 enhanced 或 expert")
		}
		for _, c := range append(append([]Command{}, t.Install...), t.Uninstall...) {
			if len(c.OSID) == 0 || len(c.Argv) == 0 {
				bad(id, "命令要写 os_id 与 argv")
			}
			for _, a := range c.Argv {
				for _, v := range varRe.FindAllString(a, -1) {
					if v != "${HOME}" && v != "${USER}" {
						bad(id, "argv 只能引用 ${HOME} 与 ${USER}：%s", a)
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}
