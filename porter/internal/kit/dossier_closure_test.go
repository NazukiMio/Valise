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
	after, err := porteridentity.Read(boundHome, "bound")
	if err != nil || after.Agent == nil || after.Agent.Ref != agent.Ref || after.Agent.Roots["ext-kb"] != bNotes {
		t.Fatalf("bound identity not synced cleanly: %+v err=%v", after, err)
	}
}
