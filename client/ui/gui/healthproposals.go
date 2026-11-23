package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/agentkit"
)

// The agent's "健康" proposals card (docs/AGENT_KIT.md 8.5, docs/UI.md 7.5):
// stays quiet when empty; demotions always require an individual modal check
// and send AcceptDemotions (never Accept or bulk accept).

type proposalsOps struct {
	list   func(context.Context, string, string) ([]agentkit.Proposal, error)
	decide func(context.Context, string, agentkit.DecideParams) (*agentkit.DecideResult, error)
}

type proposalsState struct {
	started  bool
	read     job
	items    []agentkit.Proposal
	err      error
	actJ     job
	note     string
	demotion *agentkit.Proposal
}

func (a *App) propOps() proposalsOps {
	ops := proposalsOps{
		list:   a.svc.Proposals,
		decide: a.svc.DecideProposals,
	}
	if h := a.proposalsHook; h != nil {
		if h.list != nil {
			ops.list = h.list
		}
		if h.decide != nil {
			ops.decide = h.decide
		}
	}
	return ops
}

func (a *App) proposalsOf(server, instance string) *proposalsState {
	key := server + "/" + instance
	if a.proposals == nil {
		a.proposals = map[string]*proposalsState{}
	}
	st := a.proposals[key]
	if st == nil {
		st = &proposalsState{}
		a.proposals[key] = st
	}
	if !st.started {
		st.started = true
		a.readProposals(server, instance)
	}
	return st
}

func (a *App) readProposals(server, instance string) {
	key := server + "/" + instance
	if a.proposals == nil {
		a.proposals = map[string]*proposalsState{}
	}
	st := a.proposals[key]
	if st == nil {
		st = &proposalsState{started: true}
		a.proposals[key] = st
	}
	if st.read.busy {
		return
	}
	ops := a.propOps()
	var items []agentkit.Proposal
	a.start(&st.read, time.Minute, func(ctx context.Context) (err error) {
		items, err = ops.list(ctx, server, instance)
		return err
	}, func(err error) {
		st.err = err
		if err == nil {
			st.items = items
		}
	})
}

func (a *App) proposalsCard(c *ui.Context, row *agentRow) {
	key := row.Server + "/" + row.Instance
	st := a.proposalsOf(row.Server, row.Instance)
	if !st.read.busy && st.err == nil && len(st.items) == 0 && st.note == "" && !st.actJ.busy && st.actJ.err == nil {
		return
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			cardTitle(ui.Text(c, T("prop.title"))).Grow(1)
			if st.read.busy {
				ui.Spinner(c).Size(14, 14)
			}
		})
		muted(ui.Text(c, T("prop.what"))).FontSize(fsSub).Wrap()
		if st.err != nil {
			errorBox(c, st.err, a.fold("prop-err-"+key))
		}
		if st.actJ.busy {
			a.jobLine(c, &st.actJ)
		}
		if st.actJ.err != nil {
			errorBox(c, st.actJ.err, a.fold("prop-act-err-"+key))
		}
		if st.note != "" {
			notice(c, "ok", st.note)
		}
		var normal, demotions []agentkit.Proposal
		var normalIDs []string
		for _, p := range st.items {
			if p.Demotion {
				demotions = append(demotions, p)
			} else {
				normal = append(normal, p)
				normalIDs = append(normalIDs, p.ID)
			}
		}
		if len(normal) > 1 && !st.actJ.busy {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				needTag(c, T("need.valise"))
				ui.Button(c, T("prop.accept_all_normal")).OnClick(func() {
					a.runProposalsDecide(row.Server, row.Instance, st, agentkit.DecideParams{InstanceID: row.Instance, Accept: normalIDs})
				})
				quietButton(c, T("prop.reject_all_normal")).OnClick(func() {
					a.runProposalsDecide(row.Server, row.Instance, st, agentkit.DecideParams{InstanceID: row.Instance, Reject: normalIDs})
				})
			})
		}
		for _, p := range normal {
			prop := p
			subPanel(ui.Column(c.Key("prop-" + prop.ID))).Gap(6).Children(func() {
				proposalHead(c, prop)
				if prop.Reason != "" {
					ui.Text(c, prop.Reason).FontSize(fsSub).Wrap().Selectable()
				}
				switch prop.EffectiveKind() {
				case agentkit.ProposalKindSQLite:
					if prop.SQLite != nil && len(prop.SQLite.SecretTables) > 0 {
						muted(ui.Text(c, T("prop.sqlite_tables", "tables", strings.Join(prop.SQLite.SecretTables, ", ")))).FontSize(fsSub).Wrap().Selectable()
					}
				case agentkit.ProposalKindExternalRef:
					if prop.ExternalRef != nil {
						kindLabel := T("prop.ext_dir")
						if prop.ExternalRef.RefKind == agentkit.ExternalRefSymlink {
							kindLabel = T("prop.ext_symlink")
						}
						muted(ui.Text(c, kindLabel)).FontSize(fsSub)
						notice(c, "warn", T("prop.ext_note"))
					}
				default:
					if prop.Unclassified > 0 {
						notice(c, "warn", T("prop.unclassified_warn", "n", prop.Unclassified))
					}
				}
				if !st.actJ.busy {
					acceptBtn := T("prop.accept")
					if prop.EffectiveKind() == agentkit.ProposalKindSQLite {
						acceptBtn = T("prop.sqlite_accept")
					} else if prop.EffectiveKind() == agentkit.ProposalKindExternalRef {
						acceptBtn = T("prop.ext_accept")
					}
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						needTag(c, T("need.valise"))
						ui.PrimaryButton(c, acceptBtn).OnClick(func() {
							a.runProposalsDecide(row.Server, row.Instance, st, agentkit.DecideParams{InstanceID: row.Instance, Accept: []string{prop.ID}})
						})
						quietButton(c, T("prop.reject")).OnClick(func() {
							a.runProposalsDecide(row.Server, row.Instance, st, agentkit.DecideParams{InstanceID: row.Instance, Reject: []string{prop.ID}})
						})
					})
				}
			})
		}
		for _, p := range demotions {
			prop := p
			subPanel(ui.Column(c.Key("prop-"+prop.ID))).Gap(6).Border(1, palette.TroubleBorder).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					plainTag(c, T("prop.demotion_tag"))
					muted(ui.Text(c, when(prop.Created, c.Now()))).FontSize(fsSub)
				})
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					mono(ui.Text(c, prop.Rule.Root+":"+prop.Rule.Glob+" -> "+prop.Rule.Category)).FontSize(fsSub).Grow(1).Wrap().Selectable()
					if lbl := proposalCategoryLabel(prop.Rule.Category); lbl != "" {
						plainTag(c, lbl)
					}
				})
				if prop.Reason != "" {
					ui.Text(c, prop.Reason).FontSize(fsSub).Wrap().Selectable()
				}
				muted(ui.Text(c, T("prop.demotion_brief"))).FontSize(fsSub).Wrap()
				if !st.actJ.busy {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Button(c, T("prop.demotion_open")).OnClick(func() { cp := prop; st.demotion = &cp })
						quietButton(c, T("prop.reject")).OnClick(func() {
							a.runProposalsDecide(row.Server, row.Instance, st, agentkit.DecideParams{InstanceID: row.Instance, Reject: []string{prop.ID}})
						})
					})
				}
			})
		}
	})
	a.demotionConfirmModal(c, row, st)
}

func proposalHead(c *ui.Context, prop agentkit.Proposal) {
	switch prop.EffectiveKind() {
	case agentkit.ProposalKindSQLite:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			plainTag(c, T("prop.sqlite_tag"))
			muted(ui.Text(c, when(prop.Created, c.Now()))).FontSize(fsSub)
		})
		if prop.SQLite != nil {
			mono(ui.Text(c, prop.SQLite.Root+":"+prop.SQLite.Glob)).FontSize(fsSub).Wrap().Selectable()
		}
	case agentkit.ProposalKindExternalRef:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			plainTag(c, T("prop.ext_tag"))
			muted(ui.Text(c, when(prop.Created, c.Now()))).FontSize(fsSub)
		})
		if prop.ExternalRef != nil {
			mono(ui.Text(c, prop.ExternalRef.Path)).FontSize(fsSub).Wrap().Selectable()
		}
	default:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			mono(ui.Text(c, prop.Rule.Root+":"+prop.Rule.Glob+" -> "+prop.Rule.Category)).FontSize(fsSub).Grow(1).Wrap().Selectable()
			if lbl := proposalCategoryLabel(prop.Rule.Category); lbl != "" {
				plainTag(c, lbl)
			}
			muted(ui.Text(c, when(prop.Created, c.Now()))).FontSize(fsSub)
		})
	}
}

func proposalCategoryLabel(cat string) string {
	for _, ch := range categoryChoices {
		if ch.id == cat && ch.id != "" {
			return T(ch.label)
		}
	}
	return ""
}

func (a *App) demotionConfirmModal(c *ui.Context, row *agentRow, st *proposalsState) {
	p := st.demotion
	if p == nil {
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 460).Children(func() {
			dialogTitle(ui.Text(c, T("prop.demotion_title")))
			notice(c, "warn", T("prop.demotion_warn"))
			subPanel(ui.Column(c)).Gap(4).Children(func() {
				mono(ui.Text(c, p.Rule.Root+":"+p.Rule.Glob+" -> "+p.Rule.Category)).FontSize(fsSub).Wrap().Selectable()
				if p.Reason != "" {
					ui.Text(c, p.Reason).FontSize(fsSub).Wrap().Selectable()
				}
			})
			if st.actJ.busy {
				a.jobLine(c, &st.actJ)
				return
			}
			if st.actJ.err != nil {
				errorBox(c, st.actJ.err, a.fold("prop-demote-err-"+p.ID))
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, T("need.valise"))
				ui.Button(c, T("common.cancel")).OnClick(func() {
					st.demotion = nil
					st.actJ.err = nil
				})
				dangerButton(c, T("prop.demotion_confirm")).OnClick(func() {
					a.runProposalsDecide(row.Server, row.Instance, st, agentkit.DecideParams{
						InstanceID:      row.Instance,
						AcceptDemotions: []string{p.ID},
					})
				})
			})
		})
	}).Dismissed() && !st.actJ.busy {
		st.demotion = nil
		st.actJ.err = nil
	}
}

func (a *App) runProposalsDecide(server, instance string, st *proposalsState, params agentkit.DecideParams) {
	ops := a.propOps()
	var res *agentkit.DecideResult
	a.start(&st.actJ, 2*time.Minute, func(ctx context.Context) (err error) {
		res, err = ops.decide(ctx, server, params)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		st.demotion = nil
		if res != nil {
			st.note = T("prop.decided_note", "accepted", len(res.Accepted), "rejected", len(res.Rejected), "remaining", res.Remaining)
		}
		a.readProposals(server, instance)
	})
}
