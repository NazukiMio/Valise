package seal

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/valise"
	"valise/core/vault"
	"valise/porter/internal/activeview"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
	"valise/porter/internal/runtimeguard"
)

func activeBackup(ctx context.Context, home, instance string) (*activeview.DataBinding, error) {
	id, err := porteridentity.Read(home, instance)
	if err != nil || id.IdentityState != identity.Bound || id.Agent == nil {
		// An old/unbound instance is still supported, but cannot fall back past
		// an existing managed owner with missing or mismatched identity records.
		if err := runtimeguard.CheckInstance(ctx, home, instance); err != nil {
			return nil, err
		}
		return nil, err
	}
	b, err := (activeview.Reader{Home: home}).ReadData(ctx, id.Agent.Ref)
	if err == nil && b == nil {
		// Missing current metadata does not erase an existing preparation's
		// ownership or authorize reading its old native programme as current.
		if err := runtimeguard.CheckInstance(ctx, home, instance); err != nil {
			return nil, err
		}
	}
	return b, err
}

func activeNested(e *porterenroll.Env, rec *enroll.Record, p *profile.Profile) ([]string, error) {
	var out []string
	base := rec.Roots[profile.StateRoot] + string(filepath.Separator)
	add := func(path string) {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		if strings.HasPrefix(path, base) {
			out = append(out, path)
		}
	}
	for _, path := range porterenroll.NamedDirs(e.For(p), p) {
		add(path)
	}
	records, err := porterenroll.ListAcceptedRecords(e.Home)
	if err != nil {
		return nil, err
	}
	for _, other := range records {
		if other.InstanceID != rec.InstanceID && other.Superseded == nil {
			for _, path := range other.Roots {
				add(path)
			}
		}
	}
	return out, nil
}

func programBackupError() error {
	return valise.New("active_program_backup_unverified", "无法核实当前环境的完整程序，尚未建立原样备份", "可以选择完整数据档位先保存 Agent 数据；核实当前程序后再建立原样备份")
}

// captureActiveRuntime must never inspect the sealed native programme as a
// substitute for the current private programme. Data remains backup-able even
// when programme integrity, the service or its gateway is broken.
func captureActiveRuntime(ctx context.Context, home string, b *activeview.DataBinding, at time.Time) (*vault.RuntimeRecord, enroll.Provenance, bool, error) {
	if b.Native {
		r, err := captureRuntime(ctx, home, b.Record, at)
		return r, b.Record.Provenance, false, err
	}
	r, err := runtimeLayout(home, b.Record.Roots, at)
	if err != nil {
		return nil, enroll.Provenance{}, false, err
	}
	reader := activeview.Reader{Home: home}
	if b.Program == nil || b.Program.Runtime == nil || b.Program.Runtime.Installation == nil || reader.VerifyProgram(ctx, b) != nil {
		if ctx.Err() != nil {
			return nil, enroll.Provenance{}, false, ctx.Err()
		}
		r.Unresolved = append(r.Unresolved, "active_program_unverified", "program_source_unverified", "runtime_dependencies_unverified")
		return r, enroll.Provenance{}, false, nil
	}
	// Copy before changing capture time; accepted private metadata is immutable.
	data, err := json.Marshal(b.Program.Runtime)
	if err != nil || json.Unmarshal(data, &r) != nil {
		return nil, enroll.Provenance{}, false, programBackupError()
	}
	r.CapturedAt = at.UTC()
	r.Home = vault.RuntimeRoot{Path: b.Program.Layout.Home, Resolved: b.Program.Layout.Home}
	r.Roots = make(map[string]vault.RuntimeRoot, len(b.Program.Layout.Roots))
	for id, path := range b.Program.Layout.Roots {
		r.Roots[id] = vault.RuntimeRoot{Path: path, Resolved: path}
	}
	return r, r.Installation.RegisteredProvenance, true, nil
}

func validateActiveBackup(ctx context.Context, home string, b *activeview.DataBinding, programVerified bool) error {
	if b == nil {
		return nil
	}
	r := activeview.Reader{Home: home}
	if err := r.ValidateData(ctx, b); err != nil {
		return err
	}
	if programVerified {
		// Programme evidence is immutable during this capture; a change cannot
		// silently turn an already packed raw overlay into a different source.
		if err := r.VerifyProgram(ctx, b); err != nil {
			return programBackupError()
		}
		p, err := (environments.ProgramStore{Home: home}).Load(b.Registry.Agent, b.Ref)
		if err != nil || environments.Digest(p) != b.Materials {
			return programBackupError()
		}
	}
	return nil
}
