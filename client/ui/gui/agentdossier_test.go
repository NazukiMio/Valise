package gui

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
)

func TestAgentDossierCardAddAndRemoveLocalRootsAndTools(t *testing.T) {
	if id := deriveLocalRootID("/home/me/My Notes_2026/", nil); id != "ext-my-notes-2026" || enroll.ValidRootID(id) != nil {
		t.Fatalf("deriveLocalRootID = %q", id)
	}
	if id := deriveLocalRootID("/home/me/notes", []enroll.LocalRoot{{ID: "ext-notes"}}); id != "ext-notes-2" || enroll.ValidRootID(id) != nil {
		t.Fatalf("dedup deriveLocalRootID = %q", id)
	}

	a, _ := world(t)
	tt := ui.NewTester(a.View, 1180, 1800)
	rec := &enroll.Record{
		InstanceID: "xiaoman",
		LocalRoots: []enroll.LocalRoot{{ID: "ext-docs", Path: "/home/me/docs", Category: "workspace"}},
		ToolDeps:   []enroll.ToolDep{{Name: "rg", Purpose: "search"}},
	}
	var updates []enroll.DossierUpdateParams
	a.agentDossierHook = &agentDossierOps{
		get: func(context.Context, string, string) (*enroll.Record, error) {
			cp := *rec
			cp.LocalRoots = append([]enroll.LocalRoot(nil), rec.LocalRoots...)
			cp.ToolDeps = append([]enroll.ToolDep(nil), rec.ToolDeps...)
			return &cp, nil
		},
		update: func(_ context.Context, _ string, p enroll.DossierUpdateParams) (*enroll.Record, error) {
			updates = append(updates, p)
			rec.LocalRoots = append(rec.LocalRoots, p.AddRoots...)
			for _, rid := range p.RemoveRootIDs {
				rec.LocalRoots = slices.DeleteFunc(rec.LocalRoots, func(lr enroll.LocalRoot) bool { return lr.ID == rid })
			}
			rec.ToolDeps = append(rec.ToolDeps, p.AddToolDeps...)
			for _, tn := range p.RemoveToolNames {
				rec.ToolDeps = slices.DeleteFunc(rec.ToolDeps, func(td enroll.ToolDep) bool { return td.Name == tn })
			}
			cp := *rec
			cp.LocalRoots = append([]enroll.LocalRoot(nil), rec.LocalRoots...)
			cp.ToolDeps = append([]enroll.ToolDep(nil), rec.ToolDeps...)
			return &cp, nil
		},
	}

	a.agentTab = tabManage
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("dossier.title"), "/home/me/docs", "rg", "search")

	st := a.agentDossiers["home/xiaoman"]
	st.rootPath = "/home/me/knowledge"
	st.rootCat = "memory"
	tt.Frame()
	click(t, tt, T("dossier.root_add"))
	if len(updates) != 1 || len(updates[0].AddRoots) != 1 || updates[0].AddRoots[0].ID != "ext-knowledge" || updates[0].AddRoots[0].Category != "memory" {
		t.Fatalf("add root update = %+v", updates)
	}
	hasAll(t, tt.Texts(), "/home/me/knowledge", T("dossier.root_added"))

	st.toolName = "ffmpeg"
	st.toolPurp = "transcode audio"
	tt.Frame()
	click(t, tt, T("dossier.tool_add"))
	if len(updates) != 2 || len(updates[1].AddToolDeps) != 1 || updates[1].AddToolDeps[0].Name != "ffmpeg" {
		t.Fatalf("add tool update = %+v", updates)
	}
	hasAll(t, tt.Texts(), "ffmpeg", "transcode audio", T("dossier.tool_added"))

	// Remove first LocalRoot ("ext-docs").
	click(t, tt, T("dossier.remove"))
	if len(updates) != 3 || !slices.Equal(updates[2].RemoveRootIDs, []string{"ext-docs"}) {
		t.Fatalf("remove root update = %+v", updates)
	}
}
