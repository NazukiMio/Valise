package revive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/revive"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
)

// checkDirectPolicy runs inside the existing mutation lock. A request's mode
// or target ID cannot exempt another enrolled Agent whose data it replaces.
// This is an admission check; recovery of an already stopped legacy operation
// still follows its journal, and back/discard/cleanup do not need admission.
func checkDirectPolicy(ctx context.Context, s Store, p revive.Plan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.TestOnly {
		return nil
	}
	ids := map[string]bool{}
	if p.Mode == revive.ModeRollback {
		ids[p.InstanceID] = true
	}
	roots := make([]string, 0, len(p.Roots))
	for _, root := range p.Roots {
		path, err := policyPath(root.Path)
		if err != nil {
			return directPolicyUnknown()
		}
		roots = append(roots, path)
	}
	records, err := porterenroll.ListSavedRecords(s.Home)
	if err != nil {
		return directPolicyUnknown()
	}
	for _, rec := range records {
		if rec.Superseded != nil {
			continue
		}
		for _, service := range rec.Service {
			if p.Unit != "" && service.Unit == p.Unit && (service.Scope == "" || service.Scope == "user") {
				ids[rec.InstanceID] = true
			}
		}
		for _, path := range rec.Roots {
			real, err := policyPath(path)
			if err != nil {
				return directPolicyUnknown()
			}
			for _, root := range roots {
				if within(real, root) || within(root, real) {
					ids[rec.InstanceID] = true
				}
			}
		}
	}
	for id := range ids {
		binding, err := porteridentity.Read(s.Home, id)
		if err != nil || binding.IdentityState != identity.Bound || binding.Agent == nil {
			return valise.New("identity_unbound", "无法确定待恢复 Agent 的身份和恢复方式", "先在这个 Agent 的管理与设置中重新接入，再重新准备恢复")
		}
		settings, err := (environments.Store{Home: s.Home}).Settings(binding.Agent.Ref)
		if err != nil {
			return directPolicyUnknown()
		}
		if settings.Managed || settings.Busy {
			return valise.New("environment_managed", "这个 Agent 已由运行环境记录管理，不能通过旧流程直接恢复", "回到该 Agent 的恢复记录，使用对应的环境恢复入口")
		}
		if settings.Mode != environment.Direct {
			return valise.New("environment_parallel_required", "这个 Agent 的恢复方式为并行，尚未执行直接恢复", "使用并行恢复入口；若决定直接恢复，请先在该 Agent 的管理与设置中保存直通模式")
		}
	}
	return nil
}

func directPolicyUnknown() error {
	return valise.New("environment_settings_unreadable", "无法核实待恢复 Agent 的恢复方式，尚未执行直接恢复", "检查服务器上的接入记录和恢复设置后，重新准备恢复")
}

// Resolve aliases in existing parents too, so a new child of an alias cannot
// hide its overlap. Missing tails are not created by this read-only check.
func policyPath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("invalid root")
	}
	current := path
	var suffix []string
	for {
		real, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return real, nil
		}
		if !errors.Is(err, os.ErrNotExist) || current == filepath.Dir(current) {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = filepath.Dir(current)
	}
}

// LegacyCanContinue is distinct from the ownership check for backing out.
// Once stop/exchange has begun, policy changes must not strand the operation.
func LegacyCanContinue(ctx context.Context, s Store, j *revive.Journal) bool {
	return j.State != revive.StatePlanned || checkDirectPolicy(ctx, s, j.Plan) == nil
}
