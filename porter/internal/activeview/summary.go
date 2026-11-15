package activeview

import (
	"context"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/valise"
)

// Summary contains only display observations. Private programme/root paths,
// native process details and definitions are never sent in this projection.
func (r Reader) Summary(ctx context.Context, agent identity.AgentRef) (*enroll.RuntimeState, []enroll.ServiceState, []enroll.EntryState) {
	b, err := r.Read(ctx, agent)
	if err != nil {
		v := &enroll.RuntimeState{IssueCode: "environment_binding_unverified"}
		if e, ok := valise.As(err); ok && e.Code == "environment_binding_changing" {
			v.IssueCode = e.Code
		}
		return v, nil, nil
	}
	if b == nil {
		return nil, nil, nil
	}
	v := &enroll.RuntimeState{Known: true, Slot: string(b.Ref.Slot), Revision: b.Ref.Revision, OperationID: b.Operation.ID, Phase: string(b.Operation.Phase),
		Native: b.Native != nil, Running: b.Current.Running, Stopped: b.Current.Stopped, PortOwned: b.Gateway.PortOwned, HTTPResponded: b.Gateway.HTTPResponded,
		GatewayPassed: b.Gateway.GatewayPassed, ConversationConfirmed: b.ConversationConfirmed}
	services := []enroll.ServiceState{{Scope: "user", Unit: b.Current.Unit, LoadState: "loaded", Active: b.Current.ActiveState, Enabled: b.Current.UnitFileState}}
	var entries []enroll.EntryState
	for _, en := range b.Program.Profile.Entries {
		// Other declared listeners have not been attributed to this cgroup.
		if b.Program.Profile.Environments != nil && en.ID == b.Program.Profile.Environments.GatewayEntry {
			entries = append(entries, enroll.EntryState{ID: en.ID, Host: en.Host, Port: en.Port, Listening: b.Gateway.PortOwned})
		}
	}
	return v, services, entries
}
