package service

import (
	"context"
	"slices"
	"sort"

	"valise/core/identity"
	"valise/core/journey"
)

// TimelineScanRequest keeps discovery boundaries separate from a retry's
// selected locations. Empty connections never mean 'all servers'.
type TimelineScanRequest struct {
	ConnectionIDs []string
	LocalOnly     bool
	Only          []journey.LocationRef
}

func (s *Service) ScanTimelineInScope(ctx context.Context, req TimelineScanRequest, emit func(TimelineUpdate)) error {
	connections := slices.Clone(req.ConnectionIDs)
	accept := func(loc journey.LocationRef) bool {
		switch loc.Kind {
		case "local_repo", "recovery_cache":
			return true
		case "server_vault":
			return !req.LocalOnly && slices.Contains(connections, loc.ID)
		case "legacy_target":
			return !req.LocalOnly && slices.Contains(connections, loc.AuthorityID)
		case "station", "cloud":
			return !req.LocalOnly
		}
		return false
	}
	return s.timelineReader().scanTimelineScoped(ctx, req.Only, emit, s.addRemoteTimeline, accept)
}

// JourneyConnections follows only recorded migration edges between exact
// generations already known to the caller. It does not probe other servers
// to discover a relationship, and never follows clone_from or matching names.
func JourneyConnections(items []AgentSummary, connection, instance string) []string {
	connections := map[string]bool{connection: true}
	refs := map[identity.AgentRef]bool{}
	for _, it := range items {
		if it.ConnectionID == connection && it.Instance.InstanceID == instance && it.AgentRef != nil {
			refs[*it.AgentRef] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, it := range items {
			if it.AgentRef == nil {
				continue
			}
			for _, rel := range it.Relations {
				if rel.Kind != identity.MigrationCompleted || rel.SourceAgent == nil || rel.TargetAgent != *it.AgentRef {
					continue
				}
				if !refs[rel.TargetAgent] && !refs[*rel.SourceAgent] {
					continue
				}
				for _, ref := range []identity.AgentRef{rel.TargetAgent, *rel.SourceAgent} {
					if !refs[ref] {
						refs[ref], changed = true, true
					}
				}
			}
		}
	}
	for _, it := range items {
		if it.AgentRef != nil && refs[*it.AgentRef] {
			connections[it.ConnectionID] = true
		}
	}
	var result []string
	for conn := range connections {
		if conn != "" {
			result = append(result, conn)
		}
	}
	sort.Strings(result)
	return result
}
