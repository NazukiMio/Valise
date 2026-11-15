// Package activeview resolves a stable daily runtime from private records.
// It never initializes metadata, compiles a unit, or executes a journal step.
package activeview

import (
	"context"
	"errors"
	"os"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/valise"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
)

// Reader's controller is injected by the synthetic Linux lab only. Production
// constructs Reader{Home: home}; all resources are derived on this server.
type Reader struct {
	Home    string
	Control managedruntime.Controller
}

// Binding is internal, not an RPC result or caller-supplied execution scope.
type Binding struct {
	Registry              environment.Registry
	Ref                   environment.Ref
	Operation             environment.Operation
	Record                *enroll.Record
	Program               *environments.ProgramBinding
	Definition            managedruntime.Definition
	Native                *managedruntime.NativeRef
	Current               managedruntime.Observation
	Gateway               managedruntime.GatewayObservation
	ConversationConfirmed bool
}

func unavailable() error {
	return valise.New("environment_binding_unverified", "无法核实这个 Agent 当前使用的运行环境", "重新检查这个 Agent；如果仍无法核实，请查看本次恢复的处理结果")
}

func changing() error {
	return valise.New("environment_binding_changing", "运行环境的记录或当前状态有变化，需要重新核实", "重新检查这个 Agent，再查看本次恢复的处理结果")
}

// Read returns nil only when no environment registry exists. A damaged or
// interrupted managed binding returns an error, never a native fallback.
func (r Reader) Read(ctx context.Context, agent identity.AgentRef) (*Binding, error) {
	data, err := r.ReadData(ctx, agent)
	if err != nil || data == nil {
		return nil, err
	}
	if data.parent == nil {
		return r.readInitial(ctx, data)
	}
	registry, ref, op, rec, candidate := data.Registry, data.Ref, data.Operation, data.parent, data.candidate
	instance, program := data.Record, data.Program
	if err := r.VerifyProgram(ctx, data); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, unavailable()
	}
	definition := rec.OriginalDefinition
	if candidate {
		definition = rec.CandidateDefinition
	}
	c := r.Control
	if c.Home == "" {
		c.Home = r.Home
	}
	d, err := (managedruntime.Controller{Home: r.Home}).Remembered(agent, definition)
	if err != nil {
		return nil, unavailable()
	}
	binding := &Binding{Registry: registry, Ref: ref, Operation: op, Record: instance, Program: program, Definition: d}
	binding.ConversationConfirmed = confirmedRef(registry, ref)
	// Before first cutover, A is still the frozen native process. A private
	// rollback programme existing on disk is not proof that it is running.
	if !candidate && rec.Plan.NativeOriginal != nil && (op.Phase == environment.Prepared || op.Phase == environment.Cancelled) {
		if rec.Plan.NativeSourceMaterials == "" || environments.VerifyOriginalSource(ctx, program, rec.Plan.NativeSourceMaterials) != nil {
			return nil, unavailable()
		}
		native := *rec.Plan.NativeOriginal
		binding.Native = &native
		binding.Current, err = c.ReadNative(ctx, *binding.Native)
		if err == nil && binding.Current.Running {
			// A daily query may observe a legitimate reboot/restart with the
			// same definition, executable and complete source. Only this local
			// observation changes; the old activation plan stays frozen/stale.
			native.MainPID, native.KernelStart, native.ControlGroup = binding.Current.MainPID, binding.Current.KernelStart, binding.Current.ControlGroup
		}
		if err == nil {
			err = c.RequireAbsent(ctx, d)
		}
		if err == nil {
			binding.Gateway, _ = c.CheckNativeHTTP(ctx, *binding.Native, program.Profile)
		}
	} else {
		binding.Current, err = c.Read(ctx, d)
		if err == nil && rec.Plan.ManagedSourceDigest != "" {
			source, sourceErr := activationrecord.LoadSource(r.Home, agent, rec.Plan.OperationID)
			if sourceErr != nil || environments.Digest(source) != rec.Plan.ManagedSourceDigest || source.Original != rec.Plan.Original || source.Materials != rec.Plan.OriginalMaterials || source.Definition != rec.OriginalDefinition {
				err = unavailable()
			} else if source.RetiredNative != nil {
				if source.RetiredNative.Validate(program.Profile) != nil {
					err = unavailable()
				} else {
					native, nativeErr := c.ReadNative(ctx, *source.RetiredNative)
					if nativeErr != nil || !native.Stopped || native.PendingJob || native.UnitFileState != "disabled" {
						err = unavailable()
					}
				}
			}
		}
		if err == nil && rec.Plan.NativeOriginal != nil {
			native, e := c.ReadNative(ctx, *rec.Plan.NativeOriginal)
			if e != nil || !native.Stopped || native.UnitFileState != "disabled" {
				err = unavailable()
			}
		}
		if err == nil {
			binding.Gateway, _ = c.CheckHTTP(ctx, d, program.Profile)
		}
	}
	if err != nil {
		return nil, unavailable()
	}
	if binding.Current.PendingJob || !binding.Current.Running && !binding.Current.Stopped {
		return nil, changing()
	}
	// None of the reads above may turn a concurrent cutover into a mixed
	// "current" result. Metadata is read again; no replay or reconciliation.
	if err := r.ValidateData(ctx, data); err != nil {
		return nil, err
	}
	var current managedruntime.Observation
	if binding.Native != nil {
		current, err = c.ReadNative(ctx, *binding.Native)
	} else {
		current, err = c.Read(ctx, d)
	}
	if err != nil || current != binding.Current {
		return nil, changing()
	}
	if binding.Native == nil && rec.Plan.NativeOriginal != nil {
		native, e := c.ReadNative(ctx, *rec.Plan.NativeOriginal)
		if e != nil || !native.Stopped || native.UnitFileState != "disabled" {
			return nil, changing()
		}
	}
	if binding.Native != nil && environments.VerifyOriginalSource(ctx, program, rec.Plan.NativeSourceMaterials) != nil {
		return nil, changing()
	}
	return binding, ctx.Err()
}

// A label or another Agent's dossier entry is not part of this runtime's
// binding. Do not turn those independent edits into a false cutover warning.
func sameRuntimeRecord(a, b *enroll.Record) bool {
	if a == nil || b == nil || a.InstanceID != b.InstanceID || a.ProfileID != b.ProfileID || a.ProfileVersion != b.ProfileVersion || a.Name != b.Name || b.Superseded != nil {
		return false
	}
	return environments.Digest(a.Roots) == environments.Digest(b.Roots) && environments.Digest(a.LocalOverrides) == environments.Digest(b.LocalOverrides) && environments.Digest(a.Provenance) == environments.Digest(b.Provenance) && environments.Digest(a.Env) == environments.Digest(b.Env) && environments.Digest(a.EntryPorts) == environments.Digest(b.EntryPorts)
}

func stableRef(r environment.Registry) (environment.Ref, error) {
	if op := r.Operation; op != nil {
		switch op.Phase {
		case environment.Cleaning:
			if !op.Confirmed || op.Candidate.Slot != r.Active {
				return environment.Ref{}, changing()
			}
		case environment.Building, environment.Prepared, environment.AwaitingHuman, environment.Accepted, environment.RolledBack, environment.Cancelled:
		default:
			return environment.Ref{}, changing()
		}
	}
	for _, slot := range r.Slots {
		if slot.Slot == r.Active && slot.State == environment.Active {
			return slot.Ref, nil
		}
	}
	return environment.Ref{}, unavailable()
}

func (r Reader) recordFor(reg environment.Registry, ref environment.Ref) (environment.Operation, *activationrecord.Record, bool, error) {
	var options []environment.Operation
	if reg.Operation != nil {
		options = append(options, *reg.Operation)
	}
	for i := len(reg.History) - 1; i >= 0; i-- {
		options = append(options, reg.History[i])
	}
	for _, op := range options {
		candidate := op.Candidate == ref && (op.Phase == environment.AwaitingHuman || op.Phase == environment.Accepted || op.Phase == environment.Cleaning && op.Confirmed)
		original := op.Original == ref && (op.Phase == environment.Prepared || op.Phase == environment.RolledBack || op.Phase == environment.Cancelled)
		if !candidate && !original {
			continue
		}
		rec, err := activationrecord.Load(r.Home, reg.Agent, op.ID)
		if original && op.Phase == environment.Cancelled && errors.Is(err, os.ErrNotExist) {
			source, sourceErr := activationrecord.LoadSource(r.Home, reg.Agent, op.ID)
			if sourceErr != nil || source.Original != ref {
				return op, nil, false, unavailable()
			}
			for _, owner := range reg.History {
				if !source.Owns(owner) {
					continue
				}
				history := reg
				history.Operation, history.History = &owner, nil
				bound, parent, role, sourceErr := r.recordFor(history, ref)
				if sourceErr != nil || activationrecord.Digest(parent) != source.ParentDigest {
					return op, nil, false, unavailable()
				}
				return bound, parent, role, nil
			}
			return op, nil, false, unavailable()
		}
		if err != nil || rec.Plan.Snapshot != op.Snapshot || rec.Plan.Original != op.Original || rec.Plan.Candidate != op.Candidate {
			return op, nil, false, unavailable()
		}
		if candidate && rec.State != "awaiting_human" || original && op.Phase == environment.Prepared && rec.State != "ready" && rec.State != "needs_review" || original && op.Phase == environment.RolledBack && rec.State != "rolled_back" || original && op.Phase == environment.Cancelled && rec.State != "cancelled" {
			return op, nil, false, unavailable()
		}
		return op, rec, candidate, nil
	}
	return environment.Operation{}, nil, false, unavailable()
}

func confirmedRef(r environment.Registry, ref environment.Ref) bool {
	confirmed := func(o environment.Operation) bool {
		return o.Candidate == ref && o.Confirmed && (o.Phase == environment.Accepted || o.Phase == environment.Cleaning)
	}
	if r.Operation != nil && confirmed(*r.Operation) {
		return true
	}
	for _, o := range r.History {
		if confirmed(o) {
			return true
		}
	}
	return false
}
