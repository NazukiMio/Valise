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
	"sort"
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
	// ClientArchives are downloads for this computer (docs/ARCHITECTURE.md
	// 7.3, "这台电脑登录"); never installed on a server.
	ClientArchives []ClientArchive `yaml:"client_archives" json:"client_archives,omitempty"`
	// Clouds are the cloud drives reached through this tool (rclone only).
	Clouds []Cloud `yaml:"clouds" json:"clouds,omitempty"`
}

// ClientArchive is one download for this computer.
type ClientArchive struct {
	OS     string `yaml:"os" json:"os"`
	Arch   string `yaml:"arch" json:"arch"`
	URL    string `yaml:"url" json:"url"`
	SHA256 string `yaml:"sha256" json:"sha256"`
	Member string `yaml:"member" json:"member"`
}

// Cloud access scopes.
const (
	AccessFolder  = "folder"  // only files rclone created
	AccessAccount = "account" // the whole drive
)

// Cloud is one cloud drive (docs/ARCHITECTURE.md 7.3): fixed parameters
// for rclone, never any authorization content.
type Cloud struct {
	ID         string            `yaml:"id" json:"id"`
	Name       string            `yaml:"name" json:"name"`
	RcloneType string            `yaml:"rclone_type" json:"rclone_type"`
	Params     map[string]string `yaml:"params" json:"params,omitempty"`
	Access     string            `yaml:"access" json:"access"`
	AccessNote string            `yaml:"access_note" json:"access_note"`
	// Loopback false: the drive cannot use the loopback login.
	Loopback *bool `yaml:"loopback" json:"loopback,omitempty"`
	// AskApp lets rclone ask for the user's own app (config create --all).
	AskApp  bool   `yaml:"ask_app" json:"ask_app,omitempty"`
	AppHelp string `yaml:"app_help" json:"app_help,omitempty"`
	Verify  string `yaml:"verify" json:"verify,omitempty"`
}

// UsesLoopback reports whether the loopback login applies.
func (c Cloud) UsesLoopback() bool { return c.Loopback == nil || *c.Loopback }

// Pending reports a cloud that may be shown but not added.
func (c Cloud) Pending() bool { return c.Verify == "pending" }

// Cloud finds a cloud of the tool.
func (t *Tool) Cloud(id string) *Cloud {
	for i := range t.Clouds {
		if t.Clouds[i].ID == id {
			return &t.Clouds[i]
		}
	}
	return nil
}

// ClientArchive returns this computer's download.
func (t *Tool) ClientArchive(goos, arch string) *ClientArchive {
	for i := range t.ClientArchives {
		if t.ClientArchives[i].OS == goos && t.ClientArchives[i].Arch == arch {
			return &t.ClientArchives[i]
		}
	}
	return nil
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
	typeRe   = regexp.MustCompile(`^[a-z0-9]{1,30}$`)
	keyRe    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	valueRe  = regexp.MustCompile(`^[A-Za-z0-9._:/-]{0,100}$`)
)

// secretKeys may never be fixed parameters: they are authorization content.
var secretKeys = []string{"token", "client_secret", "client_id", "pass", "password", "key", "service_account_file", "service_account_credentials"}

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

// Problems lists what is wrong with a cloud entry; porter repeats it at
// the execution boundary.
func (c Cloud) Problems() []string {
	var errs []string
	bad := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	if !idRe.MatchString(c.ID) {
		bad("云盘 id 无效：%s", c.ID)
	}
	if c.Name == "" || c.AccessNote == "" || !typeRe.MatchString(c.RcloneType) {
		bad("云盘 %s 要写 name、access_note 与 rclone_type", c.ID)
	}
	if c.Access != AccessFolder && c.Access != AccessAccount {
		bad("云盘 %s 的 access 只能是 folder 或 account", c.ID)
	}
	if c.Verify != "" && c.Verify != "pending" {
		bad("云盘 %s 的 verify 只能是 pending 或不写", c.ID)
	}
	if c.AppHelp != "" {
		if u, err := url.Parse(c.AppHelp); err != nil || u.Scheme != "https" {
			bad("云盘 %s 的 app_help 必须是 https 地址", c.ID)
		}
	}
	for k, v := range c.Params {
		secret := false
		for _, s := range secretKeys {
			secret = secret || k == s
		}
		if secret || !keyRe.MatchString(k) || !valueRe.MatchString(v) || k == "config_is_local" {
			bad("云盘 %s 的参数 %s 不允许（不能写授权内容，只能是固定的简单值）", c.ID, k)
		}
	}
	return errs
}

// CreateArgs are the arguments of `rclone config create` for a remote of
// this cloud (docs/ARCHITECTURE.md 7.3): fixed parameters in key order,
// then whether this machine's browser is used, then --all when rclone
// should ask for the user's own app.
func (c Cloud) CreateArgs(remote string, local bool) []string {
	args := []string{"config", "create", remote, c.RcloneType}
	keys := make([]string, 0, len(c.Params))
	for k := range c.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, k+"="+c.Params[k])
	}
	args = append(args, fmt.Sprintf("config_is_local=%t", local))
	if c.AskApp {
		args = append(args, "--all")
	}
	return args
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
		for _, a := range t.ClientArchives {
			u, err := url.Parse(a.URL)
			switch {
			case a.OS != "darwin" && a.OS != "linux":
				bad(id, "client_archives 的 os 只能是 darwin 或 linux")
			case a.Arch != "amd64" && a.Arch != "arm64":
				bad(id, "client_archives 的 arch 只能是 amd64 或 arm64")
			case err != nil || u.Scheme != "https" || u.Host == "":
				bad(id, "下载地址必须是 https：%s", a.URL)
			case t.Binary == nil || t.Binary.Version == "" || !strings.Contains(a.URL, t.Binary.Version):
				bad(id, "client_archives 要与锁定的版本一致：%s", a.URL)
			case !shaRe.MatchString(a.SHA256):
				bad(id, "client_archives 的 sha256 格式不对")
			case !memberRe.MatchString(a.Member) || strings.Contains(a.Member, ".."):
				bad(id, "member 路径无效")
			}
		}
		clouds := map[string]bool{}
		for _, c := range t.Clouds {
			if t.ID != "rclone" {
				bad(id, "只有 rclone 能声明 clouds")
			}
			if clouds[c.ID] {
				bad(id, "云盘 id 重复：%s", c.ID)
			}
			clouds[c.ID] = true
			for _, e := range c.Problems() {
				bad(id, "%s", e)
			}
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
