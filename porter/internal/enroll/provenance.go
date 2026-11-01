package enroll

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
)

const (
	toolTimeout = 30 * time.Second
	maxPkgDepth = 8
)

// Provenance reads where the body was installed from. Failures become
// warnings; nothing is guessed.
func Provenance(ctx context.Context, e *Env, p *profile.Profile, roots []enroll.Root) enroll.Provenance {
	e = e.For(p)
	pv := p.Provenance
	if pv == nil {
		return enroll.Provenance{Warnings: []string{"profile 没有声明来源"}}
	}
	out := enroll.Provenance{Kind: pv.Kind}
	warn := func(s string) { out.Warnings = append(out.Warnings, s) }
	switch pv.Kind {
	case profile.ProvenanceNPMGlobal:
		out.NPM = npmProvenance(ctx, e, p, pv.NPM.Package, warn)
	case profile.ProvenanceGit:
		dir := ""
		for _, r := range roots {
			if r.ID == pv.Git.Root {
				dir = filepath.Join(r.Path, filepath.FromSlash(pv.Git.Path))
			}
		}
		out.Git = gitProvenance(ctx, e, dir, warn)
	case profile.ProvenanceUVTool:
		out.Tool = uvProvenance(ctx, e, pv.UV.Package, warn)
	case profile.ProvenancePipx:
		out.Tool = pipxProvenance(ctx, e, pv.Pipx.Package, warn)
	case profile.ProvenanceBinary:
		out.Binary = binaryProvenance(e, pv.Binary.Name, warn)
	}
	return out
}

func npmProvenance(ctx context.Context, e *Env, p *profile.Profile, pkg string, warn func(string)) *enroll.NPMRecord {
	rec := &enroll.NPMRecord{Package: pkg}
	var bin string
	if p.Detect != nil {
		for _, b := range p.Detect.Binaries {
			if path, err := e.LookPath(b); err == nil {
				bin = path
				break
			}
		}
	}
	if bin == "" {
		warn("找不到 harness 的可执行文件，无法确定 npm 包的安装位置与版本")
		return rec
	}
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		warn("无法解析可执行文件的真实路径")
		return rec
	}
	dir := filepath.Dir(real)
	for i := 0; i < maxPkgDepth && dir != filepath.Dir(dir); i++ {
		var meta struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil && json.Unmarshal(b, &meta) == nil && meta.Name == pkg {
			rec.Version = meta.Version
			// <prefix>/lib/node_modules/<pkg>
			nm := filepath.Dir(dir)
			if strings.HasPrefix(pkg, "@") {
				nm = filepath.Dir(nm)
			}
			if filepath.Base(nm) == "node_modules" && filepath.Base(filepath.Dir(nm)) == "lib" {
				rec.Prefix = filepath.Dir(filepath.Dir(nm))
			}
			break
		}
		dir = filepath.Dir(dir)
	}
	if rec.Version == "" {
		// The command may be a wrapper (a private prefix's launcher):
		// look in the global root of the npm on this harness's PATH.
		if out, err := e.Run(ctx, toolTimeout, "", "npm", "root", "-g"); err == nil && out.Code == 0 {
			root := firstLine(out.Stdout)
			var meta struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(pkg), "package.json")); err == nil && json.Unmarshal(b, &meta) == nil && meta.Name == pkg {
				rec.Version = meta.Version
				if filepath.Base(root) == "node_modules" && filepath.Base(filepath.Dir(root)) == "lib" {
					rec.Prefix = filepath.Dir(filepath.Dir(root))
				}
			}
		}
	}
	if rec.Version == "" {
		warn("可执行文件不属于 npm 包 " + pkg + "，无法记录来源")
		return rec
	}
	if out, err := e.Run(ctx, toolTimeout, "", "npm", "config", "get", "registry"); err == nil && out.Code == 0 {
		if u, err := url.Parse(firstLine(out.Stdout)); err == nil && u.Scheme == "https" {
			rec.Registry = u.String()
		}
	}
	if rec.Registry == "" {
		warn("无法读取 npm registry 配置")
		return rec
	}
	out, err := e.Run(ctx, toolTimeout, "", "npm", "view", pkg+"@"+rec.Version, "dist.integrity", "--registry", rec.Registry)
	if err == nil && out.Code == 0 && strings.HasPrefix(firstLine(out.Stdout), "sha") {
		rec.Integrity = firstLine(out.Stdout)
	} else {
		warn("无法从 registry 查到 " + pkg + "@" + rec.Version + " 的 integrity")
	}
	return rec
}

func gitProvenance(ctx context.Context, e *Env, dir string, warn func(string)) *enroll.GitRecord {
	rec := &enroll.GitRecord{Path: dir}
	git := func(args ...string) (string, bool) {
		out, err := e.Run(ctx, toolTimeout, dir, append([]string{"git", "-C", dir}, args...)...)
		if err != nil || out.Code != 0 {
			return "", false
		}
		return string(out.Stdout), true
	}
	if dir == "" {
		warn("profile 声明的 git 目录无法解析")
		return rec
	}
	var ok bool
	if rec.Commit, ok = git("rev-parse", "HEAD"); !ok {
		warn("身体目录不是可读取的 git 工作树：" + dir)
		return rec
	}
	rec.Commit = strings.TrimSpace(rec.Commit)
	if remote, ok := git("remote", "get-url", "origin"); ok {
		rec.Remote = strings.TrimSpace(remote)
	} else {
		warn("git 工作树没有 origin remote，无法按来源重建")
	}
	status, ok := git("status", "--porcelain", "--untracked-files=normal")
	if !ok {
		warn("无法读取 git 状态")
		return rec
	}
	for _, line := range strings.Split(status, "\n") {
		switch {
		case strings.HasPrefix(line, "??"):
			rec.Untracked++
		case strings.TrimSpace(line) != "":
			rec.Dirty = true
		}
	}
	if rec.Untracked > 0 {
		warn("身体的 git 工作树中有未跟踪的文件，它们不会进入 body_patch")
	}
	if rec.Dirty {
		if diff, ok := git("diff", "HEAD", "--binary"); ok {
			sum := sha256.Sum256([]byte(diff))
			rec.PatchBytes, rec.PatchSHA256 = int64(len(diff)), hex.EncodeToString(sum[:])
		} else {
			warn("无法生成 body_patch")
		}
	}
	return rec
}

func uvProvenance(ctx context.Context, e *Env, pkg string, warn func(string)) *enroll.ToolRecord {
	rec := &enroll.ToolRecord{Package: pkg}
	out, err := e.Run(ctx, toolTimeout, "", "uv", "tool", "list")
	if err != nil || out.Code != 0 {
		warn("无法运行 uv tool list")
		return rec
	}
	for _, line := range strings.Split(string(out.Stdout), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == pkg && strings.HasPrefix(f[1], "v") {
			rec.Version = strings.TrimPrefix(f[1], "v")
			return rec
		}
	}
	warn("uv tool list 中没有 " + pkg)
	return rec
}

func pipxProvenance(ctx context.Context, e *Env, pkg string, warn func(string)) *enroll.ToolRecord {
	rec := &enroll.ToolRecord{Package: pkg}
	out, err := e.Run(ctx, toolTimeout, "", "pipx", "list", "--json")
	if err != nil || out.Code != 0 {
		warn("无法运行 pipx list")
		return rec
	}
	var doc struct {
		Venvs map[string]struct {
			Metadata struct {
				MainPackage struct {
					PackageVersion string `json:"package_version"`
				} `json:"main_package"`
			} `json:"metadata"`
		} `json:"venvs"`
	}
	if json.Unmarshal(out.Stdout, &doc) != nil {
		warn("无法解析 pipx list 的输出")
		return rec
	}
	if v, ok := doc.Venvs[pkg]; ok && v.Metadata.MainPackage.PackageVersion != "" {
		rec.Version = v.Metadata.MainPackage.PackageVersion
	} else {
		warn("pipx 中没有 " + pkg)
	}
	return rec
}

func binaryProvenance(e *Env, name string, warn func(string)) *enroll.BinaryRecord {
	path, err := e.LookPath(name)
	if err != nil {
		warn("在登录 PATH 中找不到 " + name)
		return nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		warn("无法解析 " + name + " 的真实路径")
		return nil
	}
	f, err := os.Open(real)
	if err != nil {
		warn("无法读取 " + real)
		return nil
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		warn("无法读取 " + real)
		return nil
	}
	return &enroll.BinaryRecord{Path: real, SHA256: hex.EncodeToString(h.Sum(nil))}
}
