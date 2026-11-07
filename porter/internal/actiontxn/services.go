package actiontxn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"time"

	"valise/core/action"
	"valise/core/profile"
	"valise/porter/internal/actionjournal"
	porterenroll "valise/porter/internal/enroll"
)

var unitName = regexp.MustCompile(`^[A-Za-z0-9@._:-]+\.service$`)

func serviceState(ctx context.Context, e *porterenroll.Env, unit string) (string, string, error) {
	if !unitName.MatchString(unit) {
		return "", "", reject("服务名称无效")
	}
	out, err := e.Run(ctx, 15*time.Second, "", "systemctl", "--user", "show", unit, "--property=Id,LoadState,ActiveState,FragmentPath,DropInPaths")
	if err != nil || out.Code != 0 {
		return "", "", reject("无法核实服务状态")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(out.Stdout), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[k] = v
		}
	}
	if values["LoadState"] == "not-found" {
		return "missing", "", nil
	}
	if values["LoadState"] != "loaded" || values["Id"] != unit {
		return "", "", reject("服务身份发生变化或单元未加载")
	}
	files := append([]string{values["FragmentPath"]}, strings.Fields(values["DropInPaths"])...)
	parts := []string{unit}
	for _, path := range files {
		if !strings.HasPrefix(path, "/") || strings.Contains(path, `\`) {
			return "", "", reject("无法核实服务单元路径")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", "", reject("无法核实服务单元文件")
		}
		h := sha256.Sum256(b)
		parts = append(parts, path, hex.EncodeToString(h[:]))
	}
	identity := digest(parts)
	return values["ActiveState"], identity, nil
}
func observeServices(ctx context.Context, e *porterenroll.Env, p *profile.Profile) ([]action.Service, error) {
	var result []action.Service
	seen := map[string]bool{}
	for _, candidate := range p.Service.Candidates {
		if candidate.Scope != "user" {
			return nil, reject("配置事务仅支持已核实的用户服务")
		}
		unit, ok := e.Expand(candidate.Unit, nil)
		if !ok {
			return nil, reject("服务环境不完整")
		}
		if seen[unit] {
			continue
		}
		seen[unit] = true
		state, identity, err := serviceState(ctx, e, unit)
		if err != nil {
			return nil, err
		}
		if state == "missing" {
			continue
		}
		if state != "active" && state != "inactive" {
			return nil, reject("服务不是稳定的运行或停止状态")
		}
		result = append(result, action.Service{Unit: unit, Scope: "user", Original: state, Identity: identity})
	}
	if len(result) == 0 {
		return nil, reject("未发现已声明的服务")
	}
	return result, nil
}
func serviceCommand(ctx context.Context, e *porterenroll.Env, unit, verb string) error {
	out, err := e.Run(ctx, 45*time.Second, "", "systemctl", "--user", verb, unit)
	if err != nil || out.Code != 0 {
		return reject("服务" + verb + "失败")
	}
	return nil
}
func stopServices(ctx context.Context, e *porterenroll.Env, s actionjournal.Store, j *action.Operation) error {
	for _, sv := range j.Plan.Services {
		state, identity, err := serviceState(ctx, e, sv.Unit)
		if err != nil {
			return err
		}
		if identity != sv.Identity || state != sv.Original {
			return reject("服务状态在确认后发生变化")
		}
		if sv.Original == "inactive" {
			continue
		}
		j.StopIntent = sv.Unit
		j.Phase = "stopping"
		if err = s.Save(j); err != nil {
			return err
		}
		if err = serviceCommand(ctx, e, sv.Unit, "stop"); err != nil {
			return err
		}
		state, identity, err = serviceState(ctx, e, sv.Unit)
		if err != nil || identity != sv.Identity || state != "inactive" {
			return reject("停止服务后未确认停止")
		}
		j.Stopped[sv.Unit] = true
		j.StopIntent = ""
		if err = s.Save(j); err != nil {
			return err
		}
	}
	j.Phase = "stopped"
	return s.Save(j)
}
func restoreServices(ctx context.Context, e *porterenroll.Env, s actionjournal.Store, j *action.Operation) error {
	for _, sv := range j.Plan.Services {
		state, identity, err := serviceState(ctx, e, sv.Unit)
		if err != nil {
			return err
		}
		if identity != sv.Identity {
			return reject("服务身份已变化，保留现场")
		}
		stopped := j.Stopped[sv.Unit] || j.StopIntent == sv.Unit
		switch {
		case sv.Original == "inactive":
			if state != "inactive" {
				return reject("原本停止的服务被外部启动，不能覆盖现场")
			}
		case !stopped:
			if state != "active" {
				return reject("未由本操作停止的服务状态已变化")
			}
		case state == "inactive" || (state == "failed" && j.RestoreIntent == sv.Unit):
			j.RestoreIntent = sv.Unit
			j.Phase = "restoring"
			if err = s.Save(j); err != nil {
				return err
			}
			if state == "failed" {
				if err = serviceCommand(ctx, e, sv.Unit, "reset-failed"); err != nil {
					return err
				}
			}
			if err = serviceCommand(ctx, e, sv.Unit, "start"); err != nil {
				return err
			}
			state, identity, err = serviceState(ctx, e, sv.Unit)
			if err != nil || state != "active" || identity != sv.Identity {
				return reject("服务启动后未确认运行")
			}
		case state == "active":
			if j.RestoreIntent != sv.Unit && j.StopIntent != sv.Unit && !j.Restored[sv.Unit] {
				return reject("服务已被外部启动，需要重新确认现场")
			}
		default:
			return reject("服务不处于稳定状态，保留现场")
		}
		// Persist intent as completed without changing the frozen original state.
		if stopped {
			if j.Restored == nil {
				j.Restored = map[string]bool{}
			}
			j.Restored[sv.Unit] = true
			if j.RestoreIntent == sv.Unit {
				j.RestoreIntent = ""
			}
			if err = s.Save(j); err != nil {
				return err
			}
		}
	}
	j.Services = "restored"
	j.RecoveryFailure = ""
	return s.Save(j)
}
