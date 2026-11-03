package profiles

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"valise/client/minisign"
	"valise/core/kittools"
	"valise/core/profile"
	"valise/core/valise"
)

// Profile pack, see docs/PROFILES.md 1.1.
const (
	PackFormat    = "valise-profile-pack/1"
	PackAsset     = "valise-profiles.tar.gz"
	PackSigAsset  = PackAsset + ".minisig"
	PackTagPrefix = "profiles-"
	manifestName  = "PACK.json"
	kitToolsName  = "kit-tools.yaml"
	maxPackBytes  = 4 << 20
	maxPackFiles  = 256
)

// Manifest is PACK.json.
type Manifest struct {
	Format    string          `json:"format"`
	Version   string          `json:"version"`
	CreatedAt time.Time       `json:"created_at"`
	Profiles  []ManifestEntry `json:"profiles"`
	// KitTools is the Valise Kit tool list (docs/AGENT_KIT.md 8.4.1).
	KitTools *ManifestFile `json:"kit_tools,omitempty"`
}

// ManifestFile is a file in the pack other than a profile.
type ManifestFile struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

// ManifestEntry describes one profile file in the pack.
type ManifestEntry struct {
	File           string `json:"file"`
	ID             string `json:"id"`
	ProfileVersion int    `json:"profile_version"`
	SHA256         string `json:"sha256"`
}

// Pack is a parsed, hash-checked pack. Its profiles are not yet trusted:
// trust comes only from Verify.
type Pack struct {
	Manifest Manifest
	Docs     []*profile.Document
	// Kit is the Kit tool list, nil in packs without one.
	Kit   *kittools.List
	files map[string][]byte
}

// PackDir holds the installed pack: the archive and its signature, kept as
// downloaded and verified again on every load.
func PackDir(dataDir string) string { return filepath.Join(dataDir, "profile-pack") }

// Build packs every *.yaml in dir (the repository's profiles/) and the Kit
// tool list kitFile ("" for none) into a deterministic tar.gz. Signing
// happens outside, with the minisign tool.
func Build(dir, kitFile, version string, now time.Time) ([]byte, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	m := Manifest{Format: PackFormat, Version: version, CreatedAt: now.UTC().Truncate(time.Second)}
	files := map[string][]byte{}
	var docs []*profile.Document
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}
		d, err := profile.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		d.Path = filepath.Base(n)
		docs = append(docs, d)
		sum := sha256.Sum256(b)
		m.Profiles = append(m.Profiles, ManifestEntry{File: d.Path, ID: d.Profile.ID, ProfileVersion: d.Profile.ProfileVersion, SHA256: hex.EncodeToString(sum[:])})
		files[d.Path] = b
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("%s 中没有 profile", dir)
	}
	if err := lintAll(docs); err != nil {
		return nil, err
	}
	if kitFile != "" {
		b, err := os.ReadFile(kitFile)
		if err != nil {
			return nil, err
		}
		if _, err := kittools.Parse(b); err != nil {
			return nil, fmt.Errorf("%s: %w", kitFile, err)
		}
		sum := sha256.Sum256(b)
		m.KitTools = &ManifestFile{File: kitToolsName, SHA256: hex.EncodeToString(sum[:])}
		files[kitToolsName] = b
	}
	mj, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.ModTime = m.CreatedAt
	tw := tar.NewWriter(gz)
	add := func(name string, b []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), ModTime: m.CreatedAt, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return err
		}
		_, err := tw.Write(b)
		return err
	}
	if err := add(manifestName, append(mj, '\n')); err != nil {
		return nil, err
	}
	for _, e := range m.Profiles {
		if err := add(e.File, files[e.File]); err != nil {
			return nil, err
		}
	}
	if m.KitTools != nil {
		if err := add(kitToolsName, files[kitToolsName]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Verify checks the pack's minisign signature against the release key.
func Verify(tgz, sig []byte, publicKey string) error {
	if publicKey == "" {
		return valise.New("pack_key_missing", "这是开发版客户端，没有内置发布签名公钥，无法确认资料包来自官方",
			"把其中的资料作为本地 harness 资料安装（未经审核），或使用正式发布的客户端")
	}
	pk, err := minisign.ParsePublicKey(publicKey)
	if err != nil {
		return valise.New("pack_key_invalid", "客户端内置的 Release 签名公钥无效", "使用正式发布的客户端").WithDetail(err.Error())
	}
	if sig == nil {
		return valise.New("pack_unsigned", "资料包没有签名文件（"+PackSigAsset+"）", "从项目的 Release 页面一起下载签名文件，或作为本地 profile 安装")
	}
	if _, err := minisign.Verify(pk, tgz, sig); err != nil {
		return valise.New("pack_signature_invalid", "资料包的签名无效，可能被改动过", "不要使用这个文件；重新从项目的 Release 页面下载").WithDetail(err.Error())
	}
	return nil
}

// Read parses a pack and checks every file against PACK.json. It does not
// establish trust; see Verify.
func Read(tgz []byte) (*Pack, error) {
	bad := func(format string, a ...any) error {
		return valise.New("pack_invalid", "资料包格式不对", "重新下载资料包").WithDetail(fmt.Sprintf(format, a...))
	}
	if len(tgz) > maxPackBytes {
		return nil, bad("超过 %d 字节", maxPackBytes)
	}
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, bad("%v", err)
	}
	tr := tar.NewReader(io.LimitReader(gz, maxPackBytes))
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, bad("%v", err)
		}
		name := h.Name
		if h.Typeflag != tar.TypeReg || strings.Contains(name, "/") || name != path.Clean(name) || strings.HasPrefix(name, ".") {
			return nil, bad("不允许的条目 %q", name)
		}
		if name != manifestName && !strings.HasSuffix(name, ".yaml") {
			return nil, bad("不允许的文件 %q", name)
		}
		if _, dup := files[name]; dup || len(files) >= maxPackFiles {
			return nil, bad("重复或过多的条目 %q", name)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, bad("%v", err)
		}
		files[name] = b
	}
	var m Manifest
	if err := json.Unmarshal(files[manifestName], &m); err != nil {
		return nil, bad("PACK.json: %v", err)
	}
	if m.Format != PackFormat {
		return nil, bad("不支持的格式 %q", m.Format)
	}
	p := &Pack{Manifest: m, files: map[string][]byte{}}
	for _, e := range m.Profiles {
		b, ok := files[e.File]
		if !ok {
			return nil, bad("缺少 %s", e.File)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != e.SHA256 {
			return nil, bad("%s 的 sha256 与 PACK.json 不符", e.File)
		}
		d, err := profile.Parse(b)
		if err != nil {
			return nil, bad("%s: %v", e.File, err)
		}
		if d.Profile.ID != e.ID {
			return nil, bad("%s 的 id 与 PACK.json 不符", e.File)
		}
		d.Path = PackAsset + "/" + e.File
		p.Docs = append(p.Docs, d)
		p.files[e.File] = b
	}
	want := len(m.Profiles) + 1
	if m.KitTools != nil {
		want++
		b, ok := files[kitToolsName]
		sum := sha256.Sum256(b)
		if !ok || m.KitTools.File != kitToolsName || hex.EncodeToString(sum[:]) != m.KitTools.SHA256 {
			return nil, bad("%s 缺少或 sha256 与 PACK.json 不符", kitToolsName)
		}
		if p.Kit, err = kittools.Parse(b); err != nil {
			return nil, bad("%s: %v", kitToolsName, err)
		}
	}
	if len(files) != want {
		return nil, bad("包中有 PACK.json 没有列出的文件")
	}
	if err := lintAll(p.Docs); err != nil {
		return nil, valise.New("pack_lint", "资料包中的 profile 没有通过检查", "重新下载资料包，或向项目报告").WithDetail(err.Error())
	}
	return p, nil
}

func lintAll(docs []*profile.Document) error {
	var msgs []string
	for _, r := range profile.Lint(docs) {
		for _, i := range r.Issues {
			msgs = append(msgs, i.String())
		}
	}
	if len(msgs) > 0 {
		return errors.New(strings.Join(msgs, "; "))
	}
	return nil
}

// Install verifies and stores a signed pack, replacing any installed one.
func Install(dataDir string, tgz, sig []byte, publicKey string) (*Pack, error) {
	if err := Verify(tgz, sig, publicKey); err != nil {
		return nil, err
	}
	p, err := Read(tgz)
	if err != nil {
		return nil, err
	}
	dir := PackDir(dataDir)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".profile-pack-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, PackAsset), tgz, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, PackSigAsset), sig, 0o600); err != nil {
		return nil, err
	}
	old := tmp + ".old"
	if err := os.Rename(dir, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return nil, err
	}
	os.RemoveAll(old)
	return p, nil
}

// Installed returns the installed pack after verifying it again, or nil when
// none is installed.
func Installed(dataDir, publicKey string) (*Pack, error) {
	dir := PackDir(dataDir)
	tgz, err := os.ReadFile(filepath.Join(dir, PackAsset))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sig, err := os.ReadFile(filepath.Join(dir, PackSigAsset))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := Verify(tgz, sig, publicKey); err != nil {
		return nil, err
	}
	return Read(tgz)
}

// InstallLocal writes the pack's profiles into the local profile directory
// (an unsigned pack, after the user agreed). It returns the written paths.
func InstallLocal(dataDir string, p *Pack) ([]string, error) {
	dir := LocalDir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var out []string
	for _, e := range p.Manifest.Profiles {
		dst := filepath.Join(dir, e.File)
		if err := writeAtomic(dst, p.files[e.File]); err != nil {
			return out, err
		}
		out = append(out, dst)
	}
	return out, nil
}

func writeAtomic(dst string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(dst), ".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), dst)
}

// Commands lists the argv a profile may make Valise run on a server, for
// the user to review before trusting an unsigned profile.
func Commands(p *profile.Profile) []string {
	var out []string
	add := func(what string, a profile.Argv) {
		if len(a) > 0 {
			out = append(out, what+"："+strings.Join(a, " "))
		}
	}
	if p.Detect != nil {
		add("检测版本", p.Detect.VersionArgv)
	}
	for _, r := range p.Roots {
		add("查找目录 "+r.ID, r.DiscoverArgv)
	}
	if p.Body != nil {
		add("重建身体", p.Body.BootstrapArgv)
	}
	if p.Service != nil {
		add("安装服务", p.Service.InstallArgv)
	}
	if p.Health != nil {
		for _, c := range p.Health.Checks {
			add("健康检查", c.Argv)
		}
	}
	for _, l := range p.Login {
		add("登录 "+l.ID, l.Argv)
	}
	if p.Native != nil {
		add("导出", p.Native.ExportArgv)
	}
	for _, e := range p.Entries {
		if e.Auth != nil {
			add("管理页令牌 "+e.ID, e.Auth.URLArgv)
			add("管理页令牌 "+e.ID, e.Auth.TokenArgv)
		}
	}
	return out
}
