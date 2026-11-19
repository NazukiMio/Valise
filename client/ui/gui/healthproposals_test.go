package gui

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/agentkit"
	"valise/core/profile"
	"valise/ui/service"
)

func TestHealthProposalsCardAndDemotionModal(t *testing.T) {
	a, _ := world(t)
	a.health["home/xiaoman"] = &healthState{at: time.Now(), report: &service.HealthReport{}}
	tt := ui.NewTester(a.View, 1180, 1800)

	// Empty proposals list renders quietly.
	a.agentTab = tabHealth
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)
	if tt.HasText(T("prop.title")) {
		t.Fatalf("empty proposals should be quiet: %q", tt.Texts())
	}

	now := tt.Now()
	items := []agentkit.Proposal{
		{
			ID:           "p1",
			Kind:         agentkit.ProposalKindClassify,
			Rule:         profile.Override{Root: "state", Glob: "notes/*.md", Category: "memory"},
			Reason:       "long-term working notes",
			Unclassified: 2,
			Created:      now.Add(-5 * time.Minute),
		},
		{
			ID:      "p2",
			Kind:    agentkit.ProposalKindSQLite,
			SQLite:  &profile.SQLiteRef{Root: "state", Glob: "plugins/mem.db", SecretTables: []string{"tokens"}},
			Reason:  "plugin sqlite store",
			Created: now.Add(-4 * time.Minute),
		},
		{
			ID:          "p3",
			Kind:        agentkit.ProposalKindExternalRef,
			ExternalRef: &agentkit.ExternalRefSpec{Path: "/srv/shared-notes", RefKind: agentkit.ExternalRefSymlink},
			Reason:      "notes symlink target",
			Created:     now.Add(-3 * time.Minute),
		},
		{
			ID:       "p4",
			Kind:     agentkit.ProposalKindClassify,
			Rule:     profile.Override{Root: "state", Glob: "tmp/*", Category: "cache"},
			Reason:   "ephemeral build cache",
			Demotion: true,
			Created:  now.Add(-time.Minute),
		},
	}
	var decided []agentkit.DecideParams
	a.proposalsHook = &proposalsOps{
		list: func(context.Context, string, string) ([]agentkit.Proposal, error) {
			return append([]agentkit.Proposal{}, items...), nil
		},
		decide: func(_ context.Context, _ string, d agentkit.DecideParams) (*agentkit.DecideResult, error) {
			decided = append(decided, d)
			var acc []agentkit.Proposal
			var rej []string
			var keep []agentkit.Proposal
			for _, p := range items {
				switch {
				case slices.Contains(d.Accept, p.ID) || slices.Contains(d.AcceptDemotions, p.ID):
					acc = append(acc, p)
				case slices.Contains(d.Reject, p.ID):
					rej = append(rej, p.ID)
				default:
					keep = append(keep, p)
				}
			}
			items = keep
			return &agentkit.DecideResult{Accepted: acc, Rejected: rej, Remaining: len(items)}, nil
		},
	}

	click(t, tt, T("health.recheck"))
	hasAll(t, tt.Texts(),
		T("prop.title"),
		"state:notes/*.md -> memory",
		T("prop.unclassified_warn", "n", 2),
		T("prop.sqlite_tag"),
		"state:plugins/mem.db",
		T("prop.sqlite_tables", "tables", "tokens"),
		T("prop.ext_tag"),
		"/srv/shared-notes",
		T("prop.accept_all_normal"),
		T("prop.demotion_tag"),
	)

	// Demotion must open a separate confirmation modal; Cancel sends nothing.
	click(t, tt, T("prop.demotion_open"))
	hasAll(t, tt.Texts(), T("prop.demotion_title"), T("prop.demotion_warn"), T("need.valise"))
	if len(decided) != 0 {
		t.Fatalf("opening demotion modal decided unexpectedly: %+v", decided)
	}
	click(t, tt, T("common.cancel"))
	if len(decided) != 0 {
		t.Fatalf("canceling demotion modal decided unexpectedly: %+v", decided)
	}

	// Confirming demotion sends AcceptDemotions (never Accept).
	click(t, tt, T("prop.demotion_open"))
	click(t, tt, T("prop.demotion_confirm"))
	if len(decided) != 1 || len(decided[0].AcceptDemotions) != 1 || decided[0].AcceptDemotions[0] != "p4" || len(decided[0].Accept) != 0 {
		t.Fatalf("demotion decide = %+v", decided)
	}
	hasAll(t, tt.Texts(), T("prop.decided_note", "accepted", 1, "rejected", 0, "remaining", 3))

	// Accept classify proposal (p1), accept sqlite proposal (p2), reject external_ref (p3).
	click(t, tt, T("prop.accept"))
	if len(decided) != 2 || len(decided[1].Accept) != 1 || decided[1].Accept[0] != "p1" || len(decided[1].AcceptDemotions) != 0 {
		t.Fatalf("accept normal decide = %+v", decided)
	}
	click(t, tt, T("prop.sqlite_accept"))
	if len(decided) != 3 || len(decided[2].Accept) != 1 || decided[2].Accept[0] != "p2" {
		t.Fatalf("accept sqlite decide = %+v", decided)
	}
	click(t, tt, T("prop.reject"))
	if len(decided) != 4 || len(decided[3].Reject) != 1 || decided[3].Reject[0] != "p3" {
		t.Fatalf("reject external_ref decide = %+v", decided)
	}

	// Bulk accept sends only non-demotion IDs when multiple normal items remain.
	items = []agentkit.Proposal{
		{ID: "n1", Kind: agentkit.ProposalKindClassify, Rule: profile.Override{Root: "state", Glob: "a/*", Category: "memory"}, Created: now},
		{ID: "n2", Kind: agentkit.ProposalKindExternalRef, ExternalRef: &agentkit.ExternalRefSpec{Path: "/opt/ext", RefKind: agentkit.ExternalRefDir}, Created: now},
		{ID: "d1", Kind: agentkit.ProposalKindClassify, Rule: profile.Override{Root: "state", Glob: "c/*", Category: "ignore"}, Demotion: true, Created: now},
	}
	click(t, tt, T("health.recheck"))
	click(t, tt, T("prop.accept_all_normal"))
	last := decided[len(decided)-1]
	if !slices.Equal(last.Accept, []string{"n1", "n2"}) || len(last.AcceptDemotions) != 0 || len(last.Reject) != 0 {
		t.Fatalf("bulk accept normal = %+v", last)
	}
	if !tt.HasText("state:c/* -> ignore") {
		t.Fatalf("demotion should stay pending after bulk normal accept: %q", tt.Texts())
	}
}
