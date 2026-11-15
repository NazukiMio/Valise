package activeview

import (
	"context"

	"valise/porter/internal/environments"
)

// Before the formal parent is saved, the initial anchor still proves exactly
// which native data/programme is current. It never authorizes a cutover.
func (r Reader) readInitial(ctx context.Context, data *DataBinding) (*Binding, error) {
	if data.initial == nil || !data.Native || r.VerifyProgram(ctx, data) != nil {
		return nil, unavailable()
	}
	c := r.Control
	if c.Home == "" {
		c.Home = r.Home
	}
	native := data.initial.Native
	b := &Binding{Registry: data.Registry, Ref: data.Ref, Operation: data.Operation, Record: data.Record, Program: data.Program, Native: &native}
	var err error
	b.Current, err = c.ReadNative(ctx, native)
	if err != nil || b.Current.PendingJob || !b.Current.Running && !b.Current.Stopped || c.RequireAgentAbsent(ctx, data.Registry.Agent) != nil {
		return nil, unavailable()
	}
	if b.Current.Running {
		native.MainPID, native.KernelStart, native.ControlGroup = b.Current.MainPID, b.Current.KernelStart, b.Current.ControlGroup
		b.Gateway, _ = c.CheckNativeHTTP(ctx, native, data.Program.Profile)
	}
	if err := r.ValidateData(ctx, data); err != nil {
		return nil, err
	}
	last, err := c.ReadNative(ctx, native)
	if err != nil || last != b.Current || c.RequireAgentAbsent(ctx, data.Registry.Agent) != nil || environments.VerifyOriginalSource(ctx, data.Program, data.Materials) != nil {
		return nil, changing()
	}
	return b, ctx.Err()
}
