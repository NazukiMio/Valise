package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"valise/client/profiles"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
)

const genericNoSecretsWarning = "该通用实例尚未声明凭据白名单（secrets）：完整/标准加密备份正常可用；未确认凭据路径前已停用“轻装（剥离凭据）”档位。建议接入后让 Agent 运行 valise-onboard 或在档案中补充凭据规则。"

// EnrollGeneric scans (and with accept=true writes local YAML + enrolls) an
// unlisted custom harness without any built-in official profile.
func (s *Service) EnrollGeneric(ctx context.Context, host string, in profiles.GenericProfileInput, accept bool) (*EnrollResult, error) {
	id, rawYAML, eff, err := profiles.BuildGenericProfile(in)
	if err != nil {
		return nil, err
	}
	set, err := s.Profiles()
	if err == nil && set != nil {
		if existing := set.ByID[id]; existing != nil && (!existing.Local || existing.OverridesOfficial) {
			return nil, valise.New("profile_id_conflict",
				fmt.Sprintf("资料标识 %s 与官方资料冲突，请更换名称或自定义标识", id),
				"改一个显示名称，或在高级标识里填写不冲突的 custom- 名称")
		}
	}

	c, err := s.session(ctx, host)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	var det enroll.DetectResult
	if err := c.Call(ctx, enroll.MethodDetect, enroll.DetectParams{Profiles: []profile.Profile{*eff}}, &det); err != nil {
		return nil, err
	}
	if len(det.Candidates) == 0 {
		return nil, valise.New("generic_state_dir_missing",
			"服务器上找不到状态目录或目录尚未初始化："+in.StateDir,
			"确认该目录已存在于服务器用户主目录下且包含 Agent 配置/数据文件后重试")
	}
	cand := det.Candidates[0]

	var rep enroll.Report
	params := enroll.ScanParams{Profile: *eff, StateDir: cand.StateDir, InstanceID: id}
	if err := c.Call(ctx, enroll.MethodScan, params, &rep); err != nil {
		return nil, err
	}
	rep.Warnings = append(cand.Warnings, rep.Warnings...)
	rep.Warnings = append(rep.Warnings, genericNoSecretsWarning)

	res := &EnrollResult{
		Host:              host,
		Candidates:        det.Candidates,
		Report:            &rep,
		ProfileLocal:      true,
		RestoreCapability: eff.CapabilityStatus("restore"),
	}
	if accept {
		hadLocal := set != nil && set.ByID[id] != nil && set.ByID[id].Local
		var prevYAML []byte
		var hasPrev bool
		if hadLocal {
			if b, readErr := os.ReadFile(filepath.Join(profiles.LocalDir(s.Dirs.Data), id+".yaml")); readErr == nil {
				prevYAML = b
				hasPrev = true
			}
		}
		if _, err := profiles.WriteLocalFile(s.Dirs.Data, id, rawYAML); err != nil {
			return res, err
		}
		var w enroll.InstanceWriteResult
		rec := enroll.RecordFrom(&rep, time.Now())
		if err := c.Call(ctx, enroll.MethodInstanceWrite, enroll.InstanceWriteParams{Record: rec, Profile: *eff}, &w); err != nil {
			if hasPrev {
				_, _ = profiles.WriteLocalFile(s.Dirs.Data, id, prevYAML)
			} else if !hadLocal {
				_ = profiles.RemoveLocalFile(s.Dirs.Data, id)
			}
			return res, err
		}
		res.Written = w.Path
		s.invalidateAgents(host)
	}
	return res, nil
}
