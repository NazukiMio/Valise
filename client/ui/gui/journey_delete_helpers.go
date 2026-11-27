package gui

import (
	"sort"
	"strings"

	"valise/core/journey"
	"valise/core/vault"
	"valise/ui/service"
)

// snapDeleteState holds selection, per-location confirmation, preview impacts,
// and deletion outcomes for single or multi-select snapshot deletion (WP2).
type snapDeleteState struct {
	scope      string // "journey:<host>/<inst>" or "loc:<loc>/<host>/<leaf>"
	selecting  bool
	open       bool
	host       string
	serverName string
	// candidates holds snapshot IDs when opened from a backup location period.
	candidates []string
	// leafBySnap maps each snapshot ID to its vault leaf (<inst>/<period>).
	leafBySnap map[string]string
	selected   map[string]bool
	locations  []service.SnapshotDeleteLocation
	checkedLoc map[string]bool
	preview    *service.SnapshotDeletePreview
	result     *service.SnapshotDeleteBatchResult
	j          job
}

func (a *App) snapDeleteFor(scope, host, serverName string) *snapDeleteState {
	if a.snapDelete == nil || a.snapDelete.scope != scope {
		a.snapDelete = &snapDeleteState{
			scope:      scope,
			host:       host,
			serverName: serverName,
			leafBySnap: map[string]string{},
			selected:   map[string]bool{},
			checkedLoc: map[string]bool{},
		}
	}
	return a.snapDelete
}

func deleteLocKey(loc service.SnapshotDeleteLocation) string { return loc.Kind + ":" + loc.Name }

func deleteLocLabel(loc service.SnapshotDeleteLocation) string {
	switch loc.Kind {
	case service.SnapshotLocationServerVault:
		return T("snapdel.loc.server_vault")
	case service.SnapshotLocationServerTarget:
		return T("snapdel.loc.server_target", "name", loc.Name)
	case service.SnapshotLocationStation:
		return T("snapdel.loc.station", "name", loc.Name)
	case service.SnapshotLocationLocalRepo:
		return T("snapdel.loc.local_repo", "name", loc.Name)
	case service.SnapshotLocationLocalCloud:
		return T("snapdel.loc.local_cloud", "name", loc.Name)
	default:
		return orText(loc.Name, loc.Kind)
	}
}

// pointLeaf extracts a valid <instance>/<period> vault leaf from p's copy
// locators (or <instance>/savepoints for savepoints). It returns "" when no
// valid period leaf can be determined; it never fabricates an invalid YYYY-MM
// leaf name.
func pointLeaf(p journey.TimelinePoint) string {
	for _, cp := range p.Copies {
		parts := strings.Split(strings.Trim(cp.SourceLocator, "/"), "/")
		if len(parts) >= 2 {
			cand := parts[len(parts)-2] + "/" + parts[len(parts)-1]
			if _, _, ok := vault.SplitLeaf(cand); ok && cand != vault.LegacyLeaf {
				return cand
			}
		}
	}
	if p.RecordedInstanceID != "" && p.Kind == vault.KindSavepoint {
		return p.RecordedInstanceID + "/" + vault.SavepointsPeriod
	}
	return ""
}

func deleteLocationsFromPoints(points []journey.TimelinePoint, selected map[string]bool) []service.SnapshotDeleteLocation {
	seen := map[string]bool{}
	var out []service.SnapshotDeleteLocation
	add := func(kind, name string) {
		loc := service.SnapshotDeleteLocation{Kind: kind, Name: name}
		if k := deleteLocKey(loc); !seen[k] {
			seen[k] = true
			out = append(out, loc)
		}
	}
	for _, p := range points {
		if !selected[p.SnapshotRef.SnapshotID] {
			continue
		}
		for _, cp := range p.Copies {
			switch cp.LocationRef.Kind {
			case "server_vault":
				add(service.SnapshotLocationServerVault, "")
			case "cloud":
				add(service.SnapshotLocationLocalCloud, cp.LocationRef.ID)
			case "legacy_target":
				add(service.SnapshotLocationServerTarget, cp.LocationRef.ID)
			case "local_repo", "local_cloud", "station", "server_target":
				add(cp.LocationRef.Kind, cp.LocationRef.ID)
			}
		}
	}
	return out
}

func (st *snapDeleteState) selectedIDs() []string {
	var ids []string
	for id, on := range st.selected {
		if on {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (st *snapDeleteState) chosenLocations() []service.SnapshotDeleteLocation {
	var locs []service.SnapshotDeleteLocation
	for _, loc := range st.locations {
		if st.checkedLoc[deleteLocKey(loc)] {
			locs = append(locs, loc)
		}
	}
	return locs
}

func (st *snapDeleteState) unresolvedCount() int {
	n := 0
	for _, id := range st.selectedIDs() {
		if st.leafBySnap[id] == "" {
			n++
		}
	}
	return n
}

func (st *snapDeleteState) requestsByLeaf() []service.SnapshotDeleteRequest {
	locs := st.chosenLocations()
	byLeaf := map[string][]string{}
	for _, id := range st.selectedIDs() {
		if lf := st.leafBySnap[id]; lf != "" {
			byLeaf[lf] = append(byLeaf[lf], id)
		}
	}
	var leaves []string
	for lf := range byLeaf {
		leaves = append(leaves, lf)
	}
	sort.Strings(leaves)
	reqs := make([]service.SnapshotDeleteRequest, 0, len(leaves))
	for _, lf := range leaves {
		reqs = append(reqs, service.SnapshotDeleteRequest{Host: st.host, Leaf: lf, SnapshotIDs: byLeaf[lf], Locations: locs, ServerName: st.serverName})
	}
	return reqs
}
