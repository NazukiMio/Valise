package gui

import (
	"strings"

	"valise/core/enroll"
	"valise/ui/service"
)

func agentRouteParts(path string) (server, instance string, ok bool) {
	rest, found := strings.CutPrefix(path, "/agents/")
	if !found || rest == "" {
		return "", "", false
	}
	server, instance, hasSlash := strings.Cut(rest, "/")
	if !hasSlash || server == "" || instance == "" || strings.Contains(instance, "/") {
		return "", "", false
	}
	return server, instance, true
}

func (a *App) agentCanReadService(server, instance string) bool {
	for _, it := range a.agents.value.Items {
		if it.ConnectionID == server && it.Instance.InstanceID == instance {
			return it.MigratedTo == nil && it.Instance.Superseded == nil && !it.Instance.StateMissing()
		}
	}
	return false
}

// onAgentRouteChanged refreshes a cached agent's live service state once when
// entering /agents/<server>/<instance>, without polling in a render loop.
func (a *App) onAgentRouteChanged(cur string) {
	server, instance, ok := agentRouteParts(cur)
	if !ok {
		a.lastAgentRoute = ""
		return
	}
	if a.lastAgentRoute == cur {
		return
	}
	a.lastAgentRoute = cur
	a.refreshProposalsOnVisit(server, instance)
	st := a.agentServices[server+"/"+instance]
	if st == nil || st.read.busy || st.ctrlJ.busy || !a.agentCanReadService(server, instance) {
		return
	}
	a.readAgentService(server, instance)
}

// syncServiceInfoToAgents mirrors live InstanceServiceInfo back into
// a.agents.value.Items so the overview statusBoard and home cards stay current.
func (a *App) syncServiceInfoToAgents(server, instance string, info *service.InstanceServiceInfo) {
	if info == nil {
		return
	}
	for i := range a.agents.value.Items {
		it := &a.agents.value.Items[i]
		if it.ConnectionID != server || it.Instance.InstanceID != instance {
			continue
		}
		if st := a.agentServices[server+"/"+instance]; st != nil && it.Instance.ProfileVersion != 0 {
			st.profileVersion = it.Instance.ProfileVersion
		}
		if info.Unit == "" {
			return
		}
		state := enroll.ServiceState{
			Scope:     info.Scope,
			Unit:      info.Unit,
			LoadState: info.LoadState,
			Active:    info.Active,
			Enabled:   info.Enabled,
		}
		if len(it.Instance.Service) == 0 {
			it.Instance.Service = []enroll.ServiceState{state}
			return
		}
		idx := 0
		for j, s := range it.Instance.Service {
			if s.Unit == info.Unit {
				idx = j
				break
			}
		}
		it.Instance.Service[idx] = state
		return
	}
}

// syncAgentsToServiceStates mirrors refreshed agent observations into cached
// agentServiceState records, invalidating them if the unit or profile version changed.
func (a *App) syncAgentsToServiceStates(items []service.AgentSummary) {
	if len(a.agentServices) == 0 {
		return
	}
	for _, it := range items {
		st := a.agentServices[it.ConnectionID+"/"+it.Instance.InstanceID]
		if st == nil || st.info == nil {
			continue
		}
		if st.profileVersion != 0 && it.Instance.ProfileVersion != 0 && st.profileVersion != it.Instance.ProfileVersion {
			st.profileVersion = it.Instance.ProfileVersion
			st.started = false
			continue
		}
		if it.Instance.ProfileVersion != 0 {
			st.profileVersion = it.Instance.ProfileVersion
		}
		if len(it.Instance.Service) == 0 {
			if st.info.Unit != "" || st.info.HasUnit {
				st.started = false
			}
			continue
		}
		svc := it.Instance.Service[0]
		if svc.Unit != st.info.Unit {
			st.started = false
			continue
		}
		st.info.LoadState = svc.LoadState
		st.info.Active = svc.Active
		st.info.Enabled = svc.Enabled
		st.info.HasUnit = svc.Exists()
	}
}
