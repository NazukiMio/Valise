package gui

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/vault"
	"valise/ui/service"
)

// manualSavepointState holds the independent manual savepoint card's inputs
// and execution outcome, separated from the automatic protection plan (WP4).
type manualSavepointState struct {
	tier    string // "soul-only" | "soul-secrets" | "full" | "raw"
	note    string
	targets map[string]bool
	members map[string]bool
	result  *service.ManualSavepointResult
	j       job
}

func (ps *protState) manualState() *manualSavepointState {
	if ps.manual == nil {
		ps.manual = &manualSavepointState{
			tier:    vault.TierFull,
			targets: map[string]bool{},
			members: map[string]bool{},
		}
	}
	return ps.manual
}

func manualTargetKey(t service.ManualSavepointTarget) string {
	return t.Kind + ":" + t.Name
}

func manualTargetLabel(t service.ManualSavepointTarget) string {
	switch t.Kind {
	case service.SnapshotLocationLocalRepo:
		return T("snapdel.loc.local_repo", "name", t.Name)
	case service.SnapshotLocationLocalCloud:
		return T("snapdel.loc.local_cloud", "name", t.Name)
	case service.SnapshotLocationStation:
		return T("snapdel.loc.station", "name", t.Name)
	default:
		return T("snapdel.loc.server_target", "name", t.Name)
	}
}

func (a *App) manualTargetOptions(row *agentRow, ps *protState) []service.ManualSavepointTarget {
	seen := map[string]bool{}
	var out []service.ManualSavepointTarget
	add := func(kind, name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		t := service.ManualSavepointTarget{Kind: kind, Name: name}
		k := manualTargetKey(t)
		if !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	}
	serverKind := func(name string) string {
		if a.svc.IsStation(name) {
			return service.SnapshotLocationStation
		}
		return service.SnapshotLocationServerTarget
	}
	if ps != nil {
		for _, tg := range ps.serverTargets {
			add(serverKind(tg.Name), tg.Name)
		}
		for _, cp := range ps.desired().ServerCopies {
			add(serverKind(cp.TargetRef.ID), cp.TargetRef.ID)
		}
	}
	if a.loc != nil && a.loc.statuses[row.Server] != nil {
		for _, tg := range a.loc.statuses[row.Server].Targets {
			add(serverKind(tg.Name), tg.Name)
		}
	}
	for _, st := range a.svc.Stations() {
		add(service.SnapshotLocationStation, st.Name)
	}
	for _, r := range a.svc.Repos() {
		add(service.SnapshotLocationLocalRepo, r.Name)
	}
	for _, cl := range a.svc.Config.Clouds {
		add(service.SnapshotLocationLocalCloud, cl.Name)
	}
	return out
}

func (a *App) manualSavepointCard(c *ui.Context, row *agentRow, ps *protState) {
	ms := ps.manualState()
	peers := sameHarnessPeers(a.agentRows(a.agents.value), *row)
	targets := a.manualTargetOptions(row, ps)
	card(ui.Column(c)).Gap(spItem).Children(func() {
		note, noteErr := vault.NormalizeNote(ms.note)
		noteOK := noteErr == nil
		ui.Row(c).Gap(spInside).AlignItems(ui.Center).Wrap().Children(func() {
			sectionTitle(ui.Text(c, T("manual.title")))
			plainTag(c, T("manual.tag"))
			ui.Button(c, T("manual.create")).Disabled(ms.j.busy || !noteOK).OnClick(func() {
				a.needKey(func() { a.runManualSavepoint(row, ps, note, targets) })
			})
		})
		muted(ui.Text(c, T("manual.hint"))).FontSize(fsSub).Wrap()
		if row.NoSecretRules && ms.tier == "soul-only" {
			ms.tier = vault.TierFull
		}
		if row.NoSecretRules {
			notice(c, "info", T("protect.tier.no_secrets_guard"))
		}
		ui.RadioGroup(c, func() {
			for _, tier := range []string{"soul-only", vault.TierSoulSecrets, vault.TierFull, vault.TierRaw} {
				if row.NoSecretRules && tier == "soul-only" {
					continue
				}
				label := tierText(tier)
				if tier == "soul-only" {
					label = tierText(vault.TierSoul)
				}
				ui.Radio(c.Key("ms-tier-"+tier), &ms.tier, tier, label)
			}
		}).Label(T("manual.tier_label"))
		muted(ui.Text(c, oneOffScopeText(manualScopeTier(ms.tier)))).FontSize(fsCaption).Wrap()
		_, _ = savepointNoteInput(c, &ms.note)

		if len(peers) > 0 {
			subPanel(ui.Column(c)).Gap(spHair).Children(func() {
				ui.Text(c, T("manual.bundle_title")).FontSize(fsSub).Bold()
				muted(ui.Text(c, T("manual.bundle_hint"))).FontSize(fsCaption).Wrap()
				for _, p := range peers {
					checked := ms.members[p.Instance]
					if ui.Checkbox(c.Key("ms-mem-"+p.Instance), &checked, p.Name).Changed() {
						ms.members[p.Instance] = checked
					}
				}
			})
		}

		if len(targets) > 0 {
			subPanel(ui.Column(c)).Gap(spHair).Children(func() {
				ui.Text(c, T("manual.targets_title")).FontSize(fsSub).Bold()
				for _, t := range targets {
					k := manualTargetKey(t)
					checked := ms.targets[k]
					if ui.Checkbox(c.Key("ms-tgt-"+k), &checked, manualTargetLabel(t)).Changed() {
						ms.targets[k] = checked
					}
				}
			})
		}

		if ms.j.busy {
			a.jobLine(c, &ms.j)
		}
		if ms.j.err != nil {
			errorBox(c, ms.j.err, a.fold("manual-sp-err-"+row.Instance))
		}
		if ms.result != nil && !ms.j.busy {
			a.manualSavepointResultView(c, ms.result)
		}
	})
}

func manualScopeTier(tier string) string {
	if tier == "soul-only" {
		return vault.TierSoul
	}
	return tier
}

func (a *App) manualSavepointResultView(c *ui.Context, res *service.ManualSavepointResult) {
	tone := "ok"
	msg := T("manual.result_ok", "n", len(res.Reports))
	if res.Partial {
		tone = "warn"
		msg = T("manual.result_partial", "n", len(res.Reports))
	}
	notice(c, tone, msg)
	if res.BundleID != "" && len(res.Members) > 1 {
		muted(ui.Text(c, T("manual.result_bundle", "members", strings.Join(res.Members, T("common.list_sep"))))).FontSize(fsCaption).Wrap()
	}
	if len(res.Syncs) > 0 {
		subPanel(ui.Column(c)).Gap(spHair).Children(func() {
			for _, sr := range res.Syncs {
				label := manualTargetLabel(sr.Target)
				if sr.OK {
					statusRow(c, "ok", label, T("manual.sync_ok"))
				} else {
					statusRow(c, "warn", label, T("manual.sync_err", "reason", sr.Error))
				}
			}
		})
	}
}

func (a *App) runManualSavepoint(row *agentRow, ps *protState, note string, available []service.ManualSavepointTarget) {
	ms := ps.manualState()
	var targets []service.ManualSavepointTarget
	for _, t := range available {
		if ms.targets[manualTargetKey(t)] {
			targets = append(targets, t)
		}
	}
	var members []string
	for id, on := range ms.members {
		if on && id != row.Instance {
			members = append(members, id)
		}
	}
	if len(members) > 0 {
		members = append(members, row.Instance)
		sort.Strings(members)
	}
	req := service.ManualSavepointRequest{
		Host:          row.Server,
		InstanceID:    row.Instance,
		Tier:          orText(ms.tier, vault.TierFull),
		BundleMembers: members,
		Note:          note,
		Targets:       targets,
	}
	fn := a.createManualSavepointFn
	if fn == nil {
		fn = a.svc.CreateManualSavepoint
	}
	var res *service.ManualSavepointResult
	a.start(&ms.j, 15*time.Minute, func(ctx context.Context) error {
		var err error
		res, err = fn(ctx, req)
		return err
	}, func(err error) {
		if err == nil {
			ms.result = res
			a.refreshTimeline()
		}
	})
}
