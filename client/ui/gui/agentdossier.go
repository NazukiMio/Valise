package gui

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
)

var localRootCategories = []struct{ id, label string }{
	{"workspace", "category.workspace"},
	{"config", "category.config"},
	{"memory", "category.memory"},
	{"identity", "category.identity"},
	{"skills", "category.skills"},
	{"state", "category.state"},
	{"secrets", "category.secrets"},
}

func isLocalRootCategory(id string) bool {
	for _, ch := range localRootCategories {
		if ch.id == id {
			return true
		}
	}
	return false
}

func localRootCategoryLabels() []string {
	out := make([]string, len(localRootCategories))
	for i, ch := range localRootCategories {
		out[i] = T(ch.label)
	}
	return out
}

func localRootCategoryLabel(id string) string {
	for _, ch := range localRootCategories {
		if ch.id == id {
			return T(ch.label)
		}
	}
	return T(localRootCategories[0].label)
}

func localRootCategoryID(label string) string {
	for _, ch := range localRootCategories {
		if T(ch.label) == label {
			return ch.id
		}
	}
	return localRootCategories[0].id
}

func proposalCategoryLabel(cat string) string {
	for _, ch := range categoryChoices {
		if ch.id == cat && ch.id != "" {
			return T(ch.label)
		}
	}
	for _, ch := range localRootCategories {
		if ch.id == cat {
			return T(ch.label)
		}
	}
	return ""
}

func deriveLocalRootID(dirPath string, existing []enroll.LocalRoot) string {
	base := filepath.Base(strings.TrimRight(strings.TrimSpace(dirPath), "/"))
	var slug strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(base) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			slug.WriteRune(r)
			prevDash = false
		default:
			if slug.Len() > 0 && !prevDash {
				slug.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(slug.String(), "-")
	if s == "" {
		s = "dir"
	}
	baseID := "ext-" + s
	if len(baseID) > 42 {
		baseID = strings.TrimRight(baseID[:42], "-")
	}
	used := map[string]bool{}
	for _, lr := range existing {
		used[lr.ID] = true
	}
	if !used[baseID] {
		return baseID
	}
	for n := 2; ; n++ {
		cand := baseID + "-" + strconv.Itoa(n)
		if !used[cand] {
			return cand
		}
	}
}

type agentDossierOps struct {
	get    func(context.Context, string, string) (*enroll.Record, error)
	update func(context.Context, string, enroll.DossierUpdateParams) (*enroll.Record, error)
}

type agentDossierState struct {
	started  bool
	read     job
	record   *enroll.Record
	err      error
	actJ     job
	note     string
	rootPath string
	rootCat  string
	toolName string
	toolPurp string
}

func (a *App) dossierOps() agentDossierOps {
	ops := agentDossierOps{
		get:    a.svc.InstanceDossier,
		update: a.svc.UpdateDossier,
	}
	if h := a.agentDossierHook; h != nil {
		if h.get != nil {
			ops.get = h.get
		}
		if h.update != nil {
			ops.update = h.update
		}
	}
	return ops
}

func (a *App) dossierOf(server, instance string) *agentDossierState {
	key := server + "/" + instance
	if a.agentDossiers == nil {
		a.agentDossiers = map[string]*agentDossierState{}
	}
	st := a.agentDossiers[key]
	if st == nil {
		st = &agentDossierState{rootCat: "workspace"}
		a.agentDossiers[key] = st
	}
	if !st.started {
		st.started = true
		a.readAgentDossier(server, instance)
	}
	return st
}

func (a *App) readAgentDossier(server, instance string) {
	key := server + "/" + instance
	if a.agentDossiers == nil {
		a.agentDossiers = map[string]*agentDossierState{}
	}
	st := a.agentDossiers[key]
	if st == nil {
		st = &agentDossierState{started: true, rootCat: "workspace"}
		a.agentDossiers[key] = st
	}
	if st.read.busy {
		return
	}
	st.started = true
	ops := a.dossierOps()
	var rec *enroll.Record
	a.start(&st.read, time.Minute, func(ctx context.Context) (err error) {
		rec, err = ops.get(ctx, server, instance)
		return err
	}, func(err error) {
		st.err = err
		if err == nil && rec != nil {
			st.record = rec
		}
	})
}

func (a *App) updateAgentDossier(row *agentRow, st *agentDossierState, params enroll.DossierUpdateParams, doneNote string, onSuccess func()) {
	if st.actJ.busy {
		return
	}
	st.note = ""
	ops := a.dossierOps()
	var rec *enroll.Record
	a.start(&st.actJ, 2*time.Minute, func(ctx context.Context) (err error) {
		rec, err = ops.update(ctx, row.Server, params)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		if rec != nil {
			st.record = rec
		}
		st.note = doneNote
		if onSuccess != nil {
			onSuccess()
		}
		if a.agentDossierHook == nil {
			a.refreshAgents()
		}
	})
}

func (a *App) agentDossierCard(c *ui.Context, row *agentRow) {
	if row == nil || row.Retired() {
		return
	}
	key := row.Server + "/" + row.Instance
	st := a.dossierOf(row.Server, row.Instance)
	if !isLocalRootCategory(st.rootCat) {
		st.rootCat = "workspace"
	}
	busy := st.read.busy || st.actJ.busy
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			sectionTitle(ui.Text(c, T("dossier.title"))).Grow(1)
			if st.read.busy {
				ui.Spinner(c).Size(14, 14)
			}
			quietButton(c, T("common.refresh")).Disabled(busy).OnClick(func() {
				st.note = ""
				a.readAgentDossier(row.Server, row.Instance)
				a.readProposals(row.Server, row.Instance)
			})
		})
		muted(ui.Text(c, T("dossier.what"))).FontSize(fsSub).Wrap()
		if st.err != nil {
			errorBox(c, st.err, a.fold("dossier-err-"+key))
		}
		if st.actJ.busy {
			a.jobLine(c, &st.actJ)
		}
		if st.actJ.err != nil {
			errorBox(c, st.actJ.err, a.fold("dossier-act-err-"+key))
		}
		if st.note != "" {
			notice(c, "ok", st.note)
		}
		a.dossierRootsSection(c, row, st, busy)
		ui.Divider(c)
		a.dossierToolsSection(c, row, st, busy)
	})
}

func (a *App) dossierRootsSection(c *ui.Context, row *agentRow, st *agentDossierState, busy bool) {
	ui.Text(c, T("dossier.roots_title")).FontSize(fsSub).Bold()
	var roots []enroll.LocalRoot
	if st.record != nil {
		roots = st.record.LocalRoots
	}
	if len(roots) == 0 {
		muted(ui.Text(c, T("dossier.roots_empty"))).FontSize(fsSub)
	} else {
		subPanel(ui.Column(c)).Gap(6).Children(func() {
			for _, lr := range roots {
				item := lr
				ui.Row(c.Key("dlr-" + item.ID)).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
					mono(ui.Text(c, item.ID)).FontSize(fsSub).Selectable()
					mono(muted(ui.Text(c, item.Path))).FontSize(fsSub).Grow(1).Wrap().Selectable()
					plainTag(c, localRootCategoryLabel(item.Category))
					quietButton(c, T("dossier.remove")).Disabled(busy).OnClick(func() {
						a.updateAgentDossier(row, st, enroll.DossierUpdateParams{
							InstanceID:    row.Instance,
							RemoveRootIDs: []string{item.ID},
						}, T("dossier.root_removed"), nil)
					})
				})
			}
		})
	}
	catLabel := localRootCategoryLabel(st.rootCat)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		ui.TextInput(c, &st.rootPath).Label(T("dossier.root_path")).Placeholder(T("dossier.root_path_placeholder")).Grow(1)
		if ui.Select(c.Key("dossier-root-cat"), &catLabel, localRootCategoryLabels()).Label(T("dossier.root_category")).Width(150).Changed() {
			st.rootCat = localRootCategoryID(catLabel)
		}
		needTag(c, T("need.valise"))
		ui.Button(c, T("dossier.root_add")).Disabled(busy || strings.TrimSpace(st.rootPath) == "").OnClick(func() {
			path := strings.TrimSpace(st.rootPath)
			id := deriveLocalRootID(path, roots)
			a.updateAgentDossier(row, st, enroll.DossierUpdateParams{
				InstanceID: row.Instance,
				AddRoots:   []enroll.LocalRoot{{ID: id, Path: path, Category: st.rootCat}},
			}, T("dossier.root_added"), func() { st.rootPath = "" })
		})
	})
}

func (a *App) dossierToolsSection(c *ui.Context, row *agentRow, st *agentDossierState, busy bool) {
	ui.Text(c, T("dossier.tools_title")).FontSize(fsSub).Bold()
	var tools []enroll.ToolDep
	if st.record != nil {
		tools = st.record.ToolDeps
	}
	if len(tools) == 0 {
		muted(ui.Text(c, T("dossier.tools_empty"))).FontSize(fsSub)
	} else {
		subPanel(ui.Column(c)).Gap(6).Children(func() {
			for _, td := range tools {
				item := td
				ui.Row(c.Key("dtd-" + item.Name)).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
					mono(ui.Text(c, item.Name)).FontSize(fsSub).Bold().Selectable()
					if item.Purpose != "" {
						muted(ui.Text(c, item.Purpose)).FontSize(fsSub).Grow(1).Wrap().Selectable()
					} else {
						ui.Spacer(c)
					}
					quietButton(c, T("dossier.remove")).Disabled(busy).OnClick(func() {
						a.updateAgentDossier(row, st, enroll.DossierUpdateParams{
							InstanceID:      row.Instance,
							RemoveToolNames: []string{item.Name},
						}, T("dossier.tool_removed"), nil)
					})
				})
			}
		})
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		ui.TextInput(c, &st.toolName).Label(T("dossier.tool_name")).Placeholder(T("dossier.tool_name_placeholder")).Width(160)
		ui.TextInput(c, &st.toolPurp).Label(T("dossier.tool_purpose")).Placeholder(T("dossier.tool_purpose_placeholder")).Grow(1)
		needTag(c, T("need.valise"))
		ui.Button(c, T("dossier.tool_add")).Disabled(busy || strings.TrimSpace(st.toolName) == "").OnClick(func() {
			name := strings.TrimSpace(st.toolName)
			purp := strings.TrimSpace(st.toolPurp)
			a.updateAgentDossier(row, st, enroll.DossierUpdateParams{
				InstanceID:  row.Instance,
				AddToolDeps: []enroll.ToolDep{{Name: name, Purpose: purp}},
			}, T("dossier.tool_added"), func() { st.toolName, st.toolPurp = "", "" })
		})
	})
}
