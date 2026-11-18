package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"valise/client/ssh"
	"valise/core/agentkit"
	"valise/core/install"
	"valise/core/kittools"
	"valise/core/valise"
)

// Valise Kit tools (docs/AGENT_KIT.md 8.4.1): only entries of the signed
// pack's list. Enhanced ones are official binaries this client downloads,
// checks and hands to porter (Valise passphrase); expert ones are fixed
// commands run in advanced mode.

// KitTool is one tool and its state on a server.
type KitTool struct {
	kittools.Tool
	Present bool   `json:"present"`
	Path    string `json:"path,omitempty"`
	// Ours: Valise installed this binary (it can remove it).
	Ours bool `json:"ours"`
	// Why says why it cannot be installed here, "" when it can.
	Why string `json:"why,omitempty"`
	// Command is the expert command for this server's distribution.
	Command   []string `json:"command,omitempty"`
	Uninstall []string `json:"uninstall,omitempty"`
}

// KitTools lists the signed list's tools with their state on host.
func (s *Service) KitTools(ctx context.Context, host string) ([]KitTool, error) {
	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	if set.Kit == nil {
		return nil, valise.New("kit_tools_none", "已安装的资料包里没有 Kit 工具清单", "安装或更新资料包：valise profiles install")
	}
	var bins []string
	for _, t := range set.Kit.Tools {
		if t.Check.Binary != "" {
			bins = append(bins, t.Check.Binary)
		}
	}
	var chk agentkit.KitToolsCheckResult
	if err := s.call(ctx, host, agentkit.MethodKitToolsCheck, agentkit.KitToolsCheckParams{Binaries: bins}, &chk); err != nil {
		return nil, err
	}
	var out []KitTool
	for _, t := range set.Kit.Tools {
		k := KitTool{Tool: t}
		if t.Check.Linger {
			k.Present = chk.Linger
		} else {
			k.Path, k.Present = chk.Found[t.Check.Binary], chk.Found[t.Check.Binary] != ""
			_, k.Ours = chk.Ours[t.Check.Binary]
		}
		switch {
		case t.Pending():
			k.Why = "还没有在靶机上核实，暂时只能自己装"
		case t.Tier == kittools.TierEnhanced && t.Binary.Archive(chk.Arch) == nil:
			k.Why = "没有 " + chk.Arch + " 架构的官方版本"
		case t.Tier == kittools.TierExpert:
			k.Command = kittools.For(t.Install, chk.OSID)
			k.Uninstall = kittools.For(t.Uninstall, chk.OSID)
			if k.Command == nil {
				k.Why = "清单里没有这台服务器发行版（" + chk.OSID + "）的安装命令"
			}
		}
		out = append(out, k)
	}
	return out, nil
}

func (s *Service) kitTool(ctx context.Context, host, id string) (*KitTool, error) {
	list, err := s.KitTools(ctx, host)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, valise.New("kit_tool_unknown", "Kit 清单里没有 "+id, "不带名称运行 valise kit tools <服务器> 查看清单")
}

// KitToolBatch is the checked command batch of an expert tool (install or
// uninstall), to show before running it in advanced mode.
func (s *Service) KitToolBatch(ctx context.Context, host, id string, uninstall bool) (*KitTool, *RequirementBatch, error) {
	k, err := s.kitTool(ctx, host, id)
	if err != nil {
		return nil, nil, err
	}
	if k.Tier != kittools.TierExpert {
		return k, nil, valise.New("kit_tool_tier", k.Name+" 不是专家级工具", "")
	}
	argv := k.Command
	if uninstall {
		if argv = k.Uninstall; argv == nil {
			return k, nil, valise.New("kit_tool_no_uninstall", "Valise 不卸载 "+k.Name+"（系统包可能被别的程序使用）", "需要时自己在服务器上卸载")
		}
	} else if k.Why != "" {
		return k, nil, valise.New("kit_tool_unavailable", k.Name+" 不能在这里安装："+k.Why, "打开服务器终端自己安装")
	}
	fs := install.CheckCommand(argv, "")
	return k, &RequirementBatch{Commands: []CheckedCommand{{Command: argv, Source: "kit", Findings: fs}},
		Refused: install.Refused(fs), Sentence: install.NeedsRiskSentence(fs)}, nil
}

// RunKitToolBatch runs a confirmed expert batch: advanced mode and the
// offsite-pod precondition (docs/INSTALL.md 4.2) are required.
func (s *Service) RunKitToolBatch(ctx context.Context, host string, b *RequirementBatch, tio InstallIO) error {
	if err := s.RequireGate(ctx, host, ""); err != nil {
		return err
	}
	return s.runAdvancedBatch(ctx, host, "kit", b, tio)
}

// InstallKitBinary downloads an enhanced tool's official build, checks its
// SHA-256, and has porter write it to ~/.local/bin.
func (s *Service) InstallKitBinary(ctx context.Context, host, id string) (*KitTool, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	k, err := s.kitTool(ctx, host, id)
	if err != nil {
		return nil, err
	}
	if k.Tier != kittools.TierEnhanced {
		return k, valise.New("kit_tool_tier", k.Name+" 是专家级工具，要在高级模式里安装", "")
	}
	if k.Why != "" {
		return k, valise.New("kit_tool_unavailable", k.Name+" 不能在这里安装："+k.Why, "打开服务器终端自己安装")
	}
	var chk agentkit.KitToolsCheckResult
	if err := s.call(ctx, host, agentkit.MethodKitToolsCheck, agentkit.KitToolsCheckParams{}, &chk); err != nil {
		return k, err
	}
	a := k.Binary.Archive(chk.Arch)
	h := s.Release.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	report(ctx, "download", 0, 0)
	data, err := s.download(ctx, h, a.URL, 512<<20)
	if err != nil {
		return k, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != a.SHA256 {
		return k, valise.New("kit_tool_sha_mismatch", "下载的 "+k.Name+" 与清单记录的 SHA-256 不一致，没有安装", "重试；仍不一致请报告").WithDetail(a.URL)
	}
	bin, err := extractMember(a.URL, data, a.Member)
	if err != nil {
		return k, valise.New("kit_tool_extract", "无法从下载的压缩包里取出 "+a.Member, "请报告").WithDetail(err.Error())
	}
	sum := sha256.Sum256(bin)
	sha := hex.EncodeToString(sum[:])
	o, err := s.ownerPorter(ctx, host)
	if err != nil {
		return k, err
	}
	var begun agentkit.KitToolBeginResult
	if err := s.callOwner(ctx, o, nil, agentkit.MethodKitToolBegin, agentkit.KitToolBeginParams{Name: k.Check.Binary, SHA256: sha}, &begun); err != nil {
		return k, err
	}
	report(ctx, "upload", 0, int64(len(bin)))
	remote := ssh.Quote(o.path) + " tool-put " + k.Check.Binary + " " + sha
	if begun.Grant != "" {
		remote += " --grant " + begun.Grant
	}
	cmd, err := o.t.Command(remote)
	if err != nil {
		return k, err
	}
	cmd.Stdin = bytes.NewReader(bin)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return k, valise.New("kit_tool_put_failed", "把 "+k.Name+" 写到服务器时失败", "看细节后重试").WithDetail(strings.TrimSpace(stderr.String()))
	}
	return s.kitTool(ctx, host, id)
}

// RemoveKitBinary removes an enhanced tool Valise installed (unchanged).
func (s *Service) RemoveKitBinary(ctx context.Context, host, id string) error {
	stopOp := s.BeginOperation()
	defer stopOp()
	k, err := s.kitTool(ctx, host, id)
	if err != nil {
		return err
	}
	if k.Tier != kittools.TierEnhanced || !k.Ours {
		return valise.New("kit_tool_not_ours", k.Name+" 不是 Valise 装的", "Valise 只删除它自己装的文件")
	}
	o, err := s.ownerPorter(ctx, host)
	if err != nil {
		return err
	}
	return s.callOwner(ctx, o, nil, agentkit.MethodKitToolRemove, agentkit.KitToolRemoveParams{Name: k.Check.Binary}, nil)
}

// extractMember returns one file from a .zip or .tar.gz download.
func extractMember(url string, data []byte, member string) ([]byte, error) {
	switch {
	case strings.HasSuffix(url, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name == member {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 512<<20))
			}
		}
	case strings.HasSuffix(url, ".tar.gz"), strings.HasSuffix(url, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Name == member && h.Typeflag == tar.TypeReg {
				return io.ReadAll(io.LimitReader(tr, 512<<20))
			}
		}
	default:
		return nil, errors.New("只支持 .zip 与 .tar.gz")
	}
	return nil, errors.New("压缩包里没有 " + member)
}
