// Package runtimeguard prevents legacy installation actions from bypassing a
// private restore/environment owner. It executes nothing and initializes no
// files. It deliberately depends on no porter enrollment/runtime executor.
package runtimeguard

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
)

type Scope struct {
	Instances     []string
	Installations []string
	Paths         []string
	Units         []profile.ServiceUnit
	All           bool
}

type occupation struct {
	instance string
	install  string
	paths    []string
	units    []profile.ServiceUnit
}

// CheckRecord also protects the installation profile shared by named Agents.
// An unchanged profile does not prevent registering a disjoint named Agent.
func CheckRecord(ctx context.Context, home string, rec enroll.Record, p *profile.Profile) error {
	occupied, err := load(home)
	if err != nil {
		return unreadable()
	}
	if len(occupied) == 0 {
		return ctx.Err()
	}
	scope := Record(rec, p)
	if p != nil {
		for _, owner := range occupied {
			if owner.install != p.ID {
				continue
			}
			var accepted profile.Profile
			if err := read(home, filepath.Join(".config/valise/dossiers", p.ID+".profile.json"), &accepted, 2<<20); err != nil {
				return unreadable()
			}
			a, _ := json.Marshal(accepted)
			b, _ := json.Marshal(p)
			if string(a) != string(b) {
				scope.Installations = append(scope.Installations, p.ID)
			}
			break
		}
	}
	return check(ctx, occupied, scope)
}

func blocked() error {
	return valise.New("environment_in_use", "这个 Agent 的运行环境由恢复记录管理，旧入口不能直接改动它", "回到该 Agent 的恢复记录处理准备、切回或清理；日常操作使用当前环境的对应入口")
}
func unreadable() error {
	return valise.New("environment_ownership_unknown", "无法核实服务器上的运行环境占用，尚未执行这次操作", "检查原恢复记录及 Valise 状态目录后重试，不要重发旧的恢复或服务命令")
}

// Check is called inside the operation's existing cross-process mutation lock.
// An unscoped execution must explicitly use All; an empty scope grants nothing.
func Check(ctx context.Context, home string, scope Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	occupied, err := load(home)
	if err != nil {
		return unreadable()
	}
	if len(occupied) == 0 {
		return nil
	}
	return check(ctx, occupied, scope)
}

func CheckInstance(ctx context.Context, home, id string) error {
	occupied, err := load(home)
	if err != nil {
		return unreadable()
	}
	if len(occupied) == 0 {
		return nil
	}
	scope, err := Instance(home, id)
	if err != nil {
		return unreadable()
	}
	return check(ctx, occupied, scope)
}

func check(ctx context.Context, occupied []occupation, scope Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var requested []string
	for _, path := range scope.Paths {
		expanded, err := aliases(path)
		if err != nil {
			return unreadable()
		}
		requested = append(requested, expanded...)
	}
	for _, owner := range occupied {
		if scope.All {
			return blocked()
		}
		for _, id := range scope.Instances {
			if id != "" && id == owner.instance {
				return blocked()
			}
		}
		for _, id := range scope.Installations {
			if id != "" && id == owner.install {
				return blocked()
			}
		}
		for _, unit := range scope.Units {
			for _, own := range owner.units {
				if unit == own {
					return blocked()
				}
			}
		}
		for _, path := range owner.paths {
			protected, err := aliases(path)
			if err != nil {
				return unreadable()
			}
			for _, a := range protected {
				for _, b := range requested {
					if overlaps(a, b) {
						return blocked()
					}
				}
			}
		}
	}
	return nil
}

func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator)) || a == string(filepath.Separator) || b == string(filepath.Separator)
}

// Resolve an existing prefix only. Missing future stage names must preserve
// their suffix so an alias cannot turn a different spelling into a bypass.
func aliases(path string) ([]string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
		return nil, unreadable()
	}
	original, current := path, path
	var suffix []string
	for {
		real, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			if real == original {
				return []string{original}, nil
			}
			return []string{original, real}, nil
		}
		if !isMissing(err) || current == filepath.Dir(current) {
			return nil, err
		}
		suffix = append(suffix, filepath.Base(current))
		current = filepath.Dir(current)
	}
}
