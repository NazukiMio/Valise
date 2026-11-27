package gui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"valise/client/config"
	"valise/core/enroll"
	"valise/core/journey"
	"valise/core/vault"
	"valise/ui/service"
)

// agentRow is one agent as the pages show it. Service state and the admin
// page's port are kept apart (AGENTS.md 7.5: no merged green badge).
type agentRow struct {
	Server, Instance, Name, Label, Initial, Harness string
	Service, ServiceTone                            string // tone: ok, stop, warn
	Entry                                           string // "" when the harness has no admin page
	Note                                            string // migrated or superseded
	MigratedToServer                                string // non-empty when migrated away
	MigratedToInstance                              string // target instance id when migrated away
	Roots                                           []string
	MissingRoots                                    []string
	StateMissing                                    bool
	ServiceUnit                                     string
	ServiceLoaded                                   bool
	ServiceActive                                   string
	ServiceSummary                                  string
	HasService                                      bool
	HasNative                                       bool
	NoSecretRules                                   bool
	Profile                                         string // profile id, for its display
	SubName                                         string // non-empty when this is a sub-agent (InstanceSummary.Name)
}

// IsSubAgent reports whether this row is a named sub-agent of a harness.
func (r agentRow) IsSubAgent() bool { return r.SubName != "" }

// Retired is an agent no longer in use here: migrated away or superseded.
func (r agentRow) Retired() bool { return r.Note != "" }

func (a *App) agentRows(res service.AgentListResult) []agentRow {
	var rows []agentRow
	for _, it := range res.Items {
		in := it.Instance
		name := in.Label
		if name == "" {
			name = in.InstanceID
		}
		r := agentRow{Server: it.ConnectionID, Instance: in.InstanceID, Name: name, Label: in.Label, Initial: initial(name),
			Harness: a.harness[in.ProfileID]}
		if r.Harness == "" {
			r.Harness = in.ProfileID
		}
		r.Service, r.ServiceTone = serviceText(in.Service)
		r.HasService, r.Profile, r.SubName = len(in.Service) > 0, in.ProfileID, in.Name
		r.HasNative = in.NativeCLI
		r.NoSecretRules = in.HasSecretRules != nil && !*in.HasSecretRules
		r.MissingRoots = append([]string(nil), in.MissingRoots...)
		r.StateMissing = in.StateMissing()
		if len(in.Service) > 0 {
			r.ServiceUnit = in.Service[0].Unit
			r.ServiceLoaded = in.Service[0].LoadState == "loaded"
			r.ServiceActive = in.Service[0].Active
			r.ServiceSummary = r.Service
		}
		if len(in.Entries) > 0 {
			r.Entry = T("entry.closed")
			for _, e := range in.Entries {
				if e.Listening {
					r.Entry = T("entry.listening")
				}
			}
		}
		switch {
		case it.MigratedTo != nil:
			r.Note = T("agent.migrated_to", "server", it.MigratedTo.ConnectionID)
			r.MigratedToServer = it.MigratedTo.ConnectionID
			r.MigratedToInstance = it.MigratedTo.AgentRef.InstanceID
		case in.Superseded != nil:
			r.Note = T("agent.superseded")
		}
		for id, p := range in.Roots {
			r.Roots = append(r.Roots, id+"  "+p)
		}
		sort.Strings(r.Roots)
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Server < rows[j].Server
	})
	return rows
}

func serviceText(units []enroll.ServiceState) (string, string) {
	if len(units) == 0 {
		return T("service.none"), "stop"
	}
	switch units[0].Active {
	case "active":
		return T("service.running"), "ok"
	case "inactive":
		return T("service.stopped"), "stop"
	case "failed":
		return T("service.failed"), "warn"
	}
	return T("service.unknown"), "warn"
}

func initial(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// pointsFor are the timeline points of one agent: recorded under that
// instance id and kept in that server's folder of a repository.
// pointsOf is pointsFor with the folder name the server's vault uses in
// repositories, which may differ from the connection's name.
func (a *App) pointsOf(conn, instance string) []journey.TimelinePoint {
	name := a.vaultNames[conn]
	if name == "" {
		name = conn
	}
	return pointsFor(a.timeline.value, name, instance)
}

func (a *App) localPointsOf(conn, instance string) []journey.TimelinePoint {
	var out []journey.TimelinePoint
	for _, p := range a.pointsOf(conn, instance) {
		for _, cp := range p.Copies {
			if cp.LocationRef.Kind == "local_repo" {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// pointsFor is an agent's saved points, newest first; server is the
// folder its server's vault has in the repositories.
func pointsFor(page service.JourneyPage, server, instance string) []journey.TimelinePoint {
	var out []journey.TimelinePoint
	for _, p := range page.Items {
		if p.RecordedInstanceID != instance {
			continue
		}
		for _, cp := range p.Copies {
			if strings.HasPrefix(cp.SourceLocator, server+"/") || cp.LocationRef.Kind == "server_vault" && cp.LocationRef.ID == server {
				out = append(out, p)
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// tierText names a backup's scope in the product's words.
func tierText(tier string) string {
	switch tier {
	case vault.TierSoul:
		return T("tier.soul")
	case vault.TierSoulSecrets:
		return T("tier.soul_secrets")
	case vault.TierFull:
		return T("tier.full")
	case vault.TierRaw:
		return T("tier.raw")
	}
	return tier
}

// tierShort is the scope in two characters, for small cards.
func tierShort(tier string) string {
	switch tier {
	case vault.TierSoul:
		return T("tier.short.soul")
	case vault.TierSoulSecrets:
		return T("tier.short.soul_secrets")
	case vault.TierFull:
		return T("tier.short.full")
	case vault.TierRaw:
		return T("tier.short.raw")
	}
	return tier
}

// when is a time the way people say it: 今天 09:00, 昨天 21:15, 11 月 3 日 10:15.
func when(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return T("time.today", "hm", t.Format("15:04"))
	case y1 == y2 && t.YearDay() == now.YearDay()-1:
		return T("time.yesterday", "hm", t.Format("15:04"))
	case y1 == y2:
		return T("time.month_day", "m", int(m1), "d", d1, "hm", t.Format("15:04"))
	}
	return T("time.date", "y", y1, "m", int(m1), "d", d1)
}

func size(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KiB", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f MiB", float64(n)/k/k)
	}
	return fmt.Sprintf("%.1f GiB", float64(n)/k/k/k)
}

// serverRow is one server on the servers page.
type serverRow struct {
	Name, Kind, Address string
	Valise              bool // added by hand: the user pass and the admin pass apply
}

func serverRows(list []config.Server) []serverRow {
	var out []serverRow
	for _, s := range list {
		r := serverRow{Name: s.Name, Kind: T("server.kind.own_ssh")}
		if s.Source == config.SourceValise {
			r.Kind, r.Valise = T("server.kind.valise"), true
			r.Address = s.HostName
			if s.User != "" {
				r.Address = s.User + "@" + r.Address
			}
		}
		out = append(out, r)
	}
	return out
}
