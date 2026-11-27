package kit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
	porteridentity "valise/porter/internal/identity"
)

func TestSafePathRejectionsAndEstimator(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".demo")
	validDir := filepath.Join(home, "notes")
	regularFile := filepath.Join(home, "plain.txt")
	os.MkdirAll(state, 0o755)
	os.MkdirAll(validDir, 0o755)
	os.WriteFile(filepath.Join(validDir, "a.md"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(validDir, "b.md"), []byte("world!"), 0o644)
	os.Symlink(filepath.Join(validDir, "a.md"), filepath.Join(validDir, "link.md"))
	os.WriteFile(regularFile, []byte("file"), 0o644)

	escapeToRoot := filepath.Join(home, "escape-root")
	os.Symlink("/", escapeToRoot)
	escapeToHome := filepath.Join(home, "escape-home")
	os.Symlink(home, escapeToHome)
	escapeToState := filepath.Join(home, "escape-state")
	os.Symlink(state, escapeToState)

	existing := map[string]string{"state": state}

	for _, bad := range []string{
		"/",
		home,
		"/etc",
		"/usr",
		"/var",
		"/tmp",
		state,
		filepath.Join(state, "sub"),
		filepath.Dir(state), // ancestor of state (also home)
		escapeToRoot,
		escapeToHome,
		escapeToState,
		regularFile,
		filepath.Join(home, "does-not-exist"),
		"relative/path",
	} {
		if _, err := ValidateLocalRootDir(home, bad, existing); err == nil {
			t.Errorf("ValidateLocalRootDir(%q) should have been rejected", bad)
		}
	}

	got, err := ValidateLocalRootDir(home, validDir, existing)
	if err != nil || got != filepath.Clean(validDir) {
		t.Fatalf("ValidateLocalRootDir(%q) = %q, %v", validDir, got, err)
	}

	files, bytes := EstimateDir(validDir)
	if files != 2 || bytes != 11 {
		t.Fatalf("EstimateDir = (%d, %d), want (2, 11)", files, bytes)
	}

	for _, badCat := range []string{profile.CategoryCache, profile.CategoryIgnore, profile.CategoryBody, "unknown", ""} {
		if AllowedLocalRootCategory(badCat) {
			t.Errorf("AllowedLocalRootCategory(%q) should be false", badCat)
		}
	}
	for _, okCat := range []string{
		profile.CategoryIdentity, profile.CategoryMemory, profile.CategoryWorkspace,
		profile.CategoryState, profile.CategorySkills, profile.CategoryConfig, profile.CategorySecrets,
	} {
		if !AllowedLocalRootCategory(okCat) {
			t.Errorf("AllowedLocalRootCategory(%q) should be true", okCat)
		}
	}

	// DeriveRootID collision suffixing.
	used := map[string]string{"state": state}
	id1 := DeriveRootID(filepath.Join(home, "My Notes!"), used)
	if id1 != "ext-my-notes" {
		t.Fatalf("id1 = %q, want ext-my-notes", id1)
	}
	used[id1] = validDir
	id2 := DeriveRootID(filepath.Join(home, "My Notes!"), used)
	if id2 != "ext-my-notes-2" {
		t.Fatalf("id2 = %q, want ext-my-notes-2", id2)
	}
}

func TestProposePromoteExternalRootsAndToolDeps(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".demo")
	notes := filepath.Join(home, "obsidian-vault")
	os.MkdirAll(state, 0o755)
	os.MkdirAll(notes, 0o755)
	os.WriteFile(filepath.Join(notes, "note.md"), []byte("12345"), 0o644)

	p := profile.Profile{ID: "demo", ProfileVersion: 1}
	rec := enroll.Record{
		InstanceID:     "demo",
		ProfileID:      "demo",
		ProfileVersion: 1,
		Roots:          map[string]string{"state": state},
		ExternalRefs:   []enroll.ExternalRef{{Path: notes, Kind: agentkit.ExternalRefDir, Reason: "old note"}},
	}
	if _, err := porterenroll.WriteRecord(home, rec, p); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC)
	lines, err := ProposeDossier(home, &rec, &p, []DossierProposalItem{
		{
			Kind:              agentkit.ProposalKindExternalRef,
			Path:              notes,
			RefKind:           agentkit.ExternalRefDir,
			SuggestedCategory: profile.CategoryMemory,
			Reason:            "Personal knowledge base",
		},
		{
			Kind:              agentkit.ProposalKindExternalRef,
			Path:              "/etc",
			RefKind:           agentkit.ExternalRefDir,
			SuggestedCategory: profile.CategoryConfig,
			Reason:            "banned system dir",
		},
		{
			Kind:              agentkit.ProposalKindExternalRef,
			Path:              notes,
			RefKind:           agentkit.ExternalRefDir,
			SuggestedCategory: profile.CategoryCache,
			Reason:            "forbidden suggested category",
		},
		{
			Kind:    agentkit.ProposalKindToolDep,
			Name:    "ffmpeg",
			Purpose: "audio conversion",
			Reason:  "voice skill needs ffmpeg",
		},
		{
			Kind:    agentkit.ProposalKindToolDep,
			Name:    "/usr/bin/bad",
			Purpose: "bad",
			Reason:  "path not allowed",
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 5 ||
		strings.Contains(lines[0], "拒绝") ||
		!strings.Contains(lines[1], "拒绝") ||
		!strings.Contains(lines[2], "拒绝") ||
		strings.Contains(lines[3], "拒绝") ||
		!strings.Contains(lines[4], "拒绝") {
		t.Fatalf("unexpected proposal lines: %v", lines)
	}

	ps, err := LoadProposals(home, "demo")
	if err != nil || len(ps) != 2 {
		t.Fatalf("pending = %+v err=%v", ps, err)
	}
	extProp, toolProp := ps[0], ps[1]
	if extProp.ExternalRef == nil || extProp.ExternalRef.SuggestedCategory != profile.CategoryMemory ||
		extProp.ExternalRef.EstimatedFiles != 1 || extProp.ExternalRef.EstimatedBytes != 5 {
		t.Fatalf("unexpected external_ref proposal: %+v", extProp)
	}
	if toolProp.ToolDep == nil || toolProp.ToolDep.Name != "ffmpeg" || toolProp.ToolDep.Purpose != "audio conversion" {
		t.Fatalf("unexpected tool_dep proposal: %+v", toolProp)
	}

	// Forbidden category on promotion must fail without mutating proposals or record.
	if _, err := Decide(home, agentkit.DecideParams{
		InstanceID: "demo",
		PromoteExternalRoots: []agentkit.ExternalRootPromotion{
			{ProposalID: extProp.ID, Category: profile.CategoryIgnore},
		},
	}); err == nil {
		t.Fatal("promoting external_ref with ignore category should fail")
	}

	// Promote external_ref + accept tool_dep together.
	res, err := Decide(home, agentkit.DecideParams{
		InstanceID: "demo",
		Accept:     []string{toolProp.ID},
		PromoteExternalRoots: []agentkit.ExternalRootPromotion{
			{ProposalID: extProp.ID},
		},
	})
	if err != nil || len(res.Accepted) != 2 || res.Remaining != 0 {
		t.Fatalf("Decide failed: %+v %v", res, err)
	}
	updated, err := porterenroll.ReadRecord(home, "demo")
	if err != nil || updated == nil {
		t.Fatalf("ReadRecord: %+v %v", updated, err)
	}
	if len(updated.LocalRoots) != 1 || updated.LocalRoots[0].ID != "ext-obsidian-vault" ||
		updated.LocalRoots[0].Category != profile.CategoryMemory || updated.Roots["ext-obsidian-vault"] != notes {
		t.Fatalf("unexpected LocalRoots/Roots: %+v %+v", updated.LocalRoots, updated.Roots)
	}
	if len(updated.ExternalRefs) != 0 {
		t.Fatalf("matching ExternalRefs entry should have been removed: %+v", updated.ExternalRefs)
	}
	if len(updated.ToolDeps) != 1 || updated.ToolDeps[0].Name != "ffmpeg" {
		t.Fatalf("unexpected ToolDeps: %+v", updated.ToolDeps)
	}

	// Classify proposal targeting the promoted LocalRoot must now validate & accept.
	os.WriteFile(filepath.Join(notes, "secret.env"), []byte("TOKEN=1"), 0o600)
	lines, err = ProposeDossier(home, updated, &p, []DossierProposalItem{
		{
			Kind:     agentkit.ProposalKindClassify,
			Root:     "ext-obsidian-vault",
			Path:     "secret.env",
			Category: profile.CategorySecrets,
			Reason:   "token file inside promoted root",
		},
	}, now)
	if err != nil || len(lines) != 1 || strings.Contains(lines[0], "拒绝") {
		t.Fatalf("classify inside local_root failed: %v %v", lines, err)
	}
	ps2, _ := LoadProposals(home, "demo")
	if _, err := Decide(home, agentkit.DecideParams{InstanceID: "demo", Accept: []string{ps2[0].ID}}); err != nil {
		t.Fatalf("Decide classify inside local_root failed: %v", err)
	}
	// Legacy unbound instance used WriteRecord (not AcceptRecord): must remain unbound after PromoteExternalRoots.
	if b := porteridentity.ListBinding(home, "demo"); b.IdentityState != identity.LegacyUnbound {
		t.Fatalf("legacy unbound instance became bound after promote: %+v", b)
	}

	// Identity-bound instance: PromoteExternalRoots must sync bound roots & preserve Generation.
	boundHome := t.TempDir()
	bState := filepath.Join(boundHome, ".bound")
	bNotes := filepath.Join(boundHome, "kb")
	os.MkdirAll(bState, 0o755)
	os.MkdirAll(bNotes, 0o755)
	bp := profile.Profile{ID: "bound", ProfileVersion: 1}
	brec := enroll.Record{InstanceID: "bound", ProfileID: "bound", ProfileVersion: 1, Roots: map[string]string{"state": bState}}
	_, agent, err := porterenroll.AcceptRecord(boundHome, brec, bp)
	if err != nil || agent == nil {
		t.Fatalf("AcceptRecord: agent=%+v err=%v", agent, err)
	}
	if _, err := ProposeDossier(boundHome, &brec, &bp, []DossierProposalItem{
		{
			Kind:              agentkit.ProposalKindExternalRef,
			Path:              bNotes,
			RefKind:           agentkit.ExternalRefDir,
			SuggestedCategory: profile.CategoryMemory,
			Reason:            "personal kb",
		},
	}, now); err != nil {
		t.Fatal(err)
	}
	bps, _ := LoadProposals(boundHome, "bound")
	if _, err := Decide(boundHome, agentkit.DecideParams{
		InstanceID:           "bound",
		PromoteExternalRoots: []agentkit.ExternalRootPromotion{{ProposalID: bps[0].ID}},
	}); err != nil {
		t.Fatalf("Decide PromoteExternalRoots on bound instance failed: %v", err)
	}
	if err := porterenroll.ValidateIdentity(context.Background(), boundHome, agent.Ref); err != nil {
		t.Fatalf("ValidateIdentity failed after PromoteExternalRoots: %v", err)
	}
	// Propose + accept web_entry on the bound instance and confirm ValidateIdentity still holds.
	if _, err := ProposeDossier(boundHome, &brec, &bp, []DossierProposalItem{
		{
			Kind:        agentkit.ProposalKindWebEntry,
			ID:          "bound-ui",
			DisplayName: "Bound UI",
			Port:        7865,
			Path:        "/",
			Hint:        "run bound ui",
			Reason:      "ui discovered",
		},
	}, now); err != nil {
		t.Fatal(err)
	}
	bwps, _ := LoadProposals(boundHome, "bound")
	if _, err := Decide(boundHome, agentkit.DecideParams{
		InstanceID: "bound",
		Accept:     []string{bwps[0].ID},
	}); err != nil {
		t.Fatalf("Decide web_entry on bound instance failed: %v", err)
	}
	if err := porterenroll.ValidateIdentity(context.Background(), boundHome, agent.Ref); err != nil {
		t.Fatalf("ValidateIdentity failed after Decide web_entry: %v", err)
	}
	after, err := porteridentity.Read(boundHome, "bound")
	if err != nil || after.Agent == nil || after.Agent.Ref != agent.Ref || after.Agent.Roots["ext-kb"] != bNotes {
		t.Fatalf("bound identity not synced cleanly: %+v err=%v", after, err)
	}
}

func TestProposeWebEntryValidationAcceptShadowAndNoSecretDowngrade(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".demo")
	os.MkdirAll(state, 0o755)
	os.WriteFile(filepath.Join(state, ".env"), []byte("KEY=1"), 0o600)

	p := profile.Profile{
		ID: "demo", ProfileVersion: 1, Status: "draft",
		Paths: []profile.PathRule{
			{Root: profile.StateRoot, Glob: ".env", Category: profile.CategorySecrets},
		},
		Entries: []profile.Entry{
			{ID: "dashboard", DisplayName: "Dashboard", Kind: "web", Host: "127.0.0.1", Port: 9000, Path: "/",
				Prepare: &profile.EntryPrepare{Steps: []profile.Argv{{"true"}}}},
		},
	}
	rec := enroll.Record{
		InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1,
		Roots: map[string]string{profile.StateRoot: state},
	}
	if _, err := porterenroll.WriteRecord(home, rec, p); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 27, 2, 0, 0, 0, time.UTC)
	lines, err := ProposeDossier(home, &rec, &p, []DossierProposalItem{
		{
			Kind:        agentkit.ProposalKindWebEntry,
			ID:          "webapp",
			DisplayName: "Web App",
			Port:        7860,
			Path:        "/ui",
			Hint:        "Run demo webapp in server shell",
			Reason:      "Detected local UI",
		},
		{
			Kind:        agentkit.ProposalKindWebEntry,
			ID:          "bad-token",
			DisplayName: "Bad",
			Port:        7861,
			Path:        "/?token=secret",
			Reason:      "token leak attempt",
		},
		{
			Kind:        agentkit.ProposalKindWebEntry,
			ID:          "bad-hint",
			DisplayName: "Bad Hint",
			Port:        7862,
			Path:        "/",
			Hint:        "open http://127.0.0.1:7862/?token=abc123",
			Reason:      "secret in hint",
		},
		{
			Kind:        agentkit.ProposalKindWebEntry,
			ID:          "dashboard",
			DisplayName: "Dup Official ID",
			Port:        7863,
			Path:        "/",
			Reason:      "conflicts with official id",
		},
		{
			Kind:        agentkit.ProposalKindWebEntry,
			ID:          "shadow-port",
			DisplayName: "Dup Official Port",
			Port:        9000,
			Path:        "/",
			Reason:      "conflicts with official port",
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 5 || strings.Contains(lines[0], "拒绝") {
		t.Fatalf("unexpected lines: %v", lines)
	}
	for i := 1; i < 5; i++ {
		if !strings.Contains(lines[i], "拒绝") {
			t.Fatalf("line %d should be rejected: %s", i, lines[i])
		}
	}

	ps, err := LoadProposals(home, "demo")
	if err != nil || len(ps) != 1 || ps[0].EffectiveKind() != agentkit.ProposalKindWebEntry {
		t.Fatalf("pending = %+v err=%v", ps, err)
	}
	if _, err := Decide(home, agentkit.DecideParams{
		InstanceID: "demo",
		Accept:     []string{ps[0].ID},
	}); err != nil {
		t.Fatalf("Decide web_entry failed: %v", err)
	}

	updated, err := porterenroll.ReadRecord(home, "demo")
	if err != nil || len(updated.LocalEntries) != 1 || updated.LocalEntries[0].ID != "webapp" || updated.LocalEntries[0].Port != 7860 {
		t.Fatalf("LocalEntries not persisted: %+v err=%v", updated, err)
	}
	effProf, err := porterenroll.ReadProfile(home, "demo")
	if err != nil || len(effProf.Entries) != 2 || effProf.Entries[1].ID != "webapp" || effProf.Entries[1].LocalSource != "agent_dossier" {
		t.Fatalf("ReadProfile missing LocalEntry: %+v err=%v", effProf, err)
	}

	// Defense-in-depth at Decide time: a forged pending proposal attempting to demote a secrets file
	// must fail Decide even when the target file is absent from disk.
	_ = os.Remove(filepath.Join(state, ".env"))
	forged := agentkit.Proposal{
		ID:      "p_forged",
		Kind:    agentkit.ProposalKindClassify,
		Rule:    profile.Override{Root: profile.StateRoot, Glob: ".env", Category: profile.CategoryMemory},
		Reason:  "bypass check when missing on disk",
		Created: now,
	}
	forgedWildcard := agentkit.Proposal{
		ID:      "p_forged_wild",
		Kind:    agentkit.ProposalKindClassify,
		Rule:    profile.Override{Root: profile.StateRoot, Glob: "**", Category: profile.CategoryMemory},
		Reason:  "wildcard bypass attempt",
		Created: now,
	}
	if err := saveProposals(home, "demo", []agentkit.Proposal{forged, forgedWildcard}); err != nil {
		t.Fatal(err)
	}
	if _, err := Decide(home, agentkit.DecideParams{InstanceID: "demo", Accept: []string{"p_forged"}}); err == nil {
		t.Fatal("Decide must reject classify proposal that demotes a secrets file even when absent on disk")
	}
	if _, err := Decide(home, agentkit.DecideParams{InstanceID: "demo", Accept: []string{"p_forged_wild"}}); err == nil {
		t.Fatal("Decide must reject wildcard classify proposal covering a secrets rule")
	}

	// Bulk accept ("all") must not implicitly accept web_entry proposals.
	wp := agentkit.Proposal{
		ID:       "p_web_bulk",
		Kind:     agentkit.ProposalKindWebEntry,
		WebEntry: &agentkit.WebEntrySpec{ID: "bulk-ui", DisplayName: "Bulk UI", Port: 7870, Path: "/"},
		Reason:   "bulk check",
		Created:  now,
	}
	if err := saveProposals(home, "demo", []agentkit.Proposal{wp}); err != nil {
		t.Fatal(err)
	}
	resBulk, err := Decide(home, agentkit.DecideParams{InstanceID: "demo", Accept: []string{"all"}})
	if err != nil || len(resBulk.Accepted) != 0 || resBulk.Remaining != 1 {
		t.Fatalf("Accept:[all] should leave web_entry pending: %+v err=%v", resBulk, err)
	}
}
