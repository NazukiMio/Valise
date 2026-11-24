package service

import (
	"slices"
	"strings"
	"testing"

	"valise/client/profiles"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
)

// A body inside the state dir that stays (or is reinstalled) travels with
// the swap, only where the target has that root; an older target porter
// may still roll back (the carry copies it) but not migrate over it
// (docs/ARCHITECTURE.md 8.2.1).
func TestNestedBodyPlan(t *testing.T) {
	prof := profile.Profile{ID: "x", DisplayName: "X",
		Paths: []profile.PathRule{{Root: "state", Glob: "config.json", Category: "secrets"}},
		Body:  &profile.Body{Paths: []profile.PathRef{{Root: "state", Glob: "tools/**"}}}}
	head := &vault.Snapshot{}
	for _, exists := range []bool{true, false} {
		rp := &RevivePlan{}
		plan := revive.Plan{Mode: revive.ModeMigrate, Roots: []revive.RootPlan{{ID: "state", Path: "/h/.x"}}}
		facts := revive.InspectResult{Roots: []revive.RootFacts{{ID: "state", Path: "/h/.x", Exists: exists}}}
		planBody(rp, &plan, head, facts, prof, revive.ModeMigrate)
		if plan.Body.Action != revive.BodyKeep || (len(plan.Body.Dirs) == 1) != exists {
			t.Fatalf("exists=%v: %+v", exists, plan.Body)
		}
	}

	for _, tc := range []struct {
		mode    string
		caps    []string
		blocked bool
		dirs    int
	}{
		{revive.ModeMigrate, nil, true, 1},
		{revive.ModeClone, nil, true, 1},
		{revive.ModeRollback, nil, false, 0},
		{revive.ModeMigrate, []string{revive.BodyDirsCapability}, false, 1},
	} {
		rp := &RevivePlan{Target: "t", targetCaps: tc.caps}
		plan := revive.Plan{Mode: tc.mode, Body: revive.BodyPlan{Action: revive.BodyKeep, Dirs: []string{"state/tools"}}}
		planNestedBody(rp, &plan)
		if (len(rp.Blockers) > 0) != tc.blocked || len(plan.Body.Dirs) != tc.dirs {
			t.Fatalf("%s caps=%v: blockers=%+v dirs=%v", tc.mode, tc.caps, rp.Blockers, plan.Body.Dirs)
		}
	}

	facts := revive.InspectResult{Roots: []revive.RootFacts{{ID: "state", Path: "/h/.x", Exists: true}}}
	for _, tc := range []struct {
		mode    string
		secrets bool
		warn    bool
	}{{revive.ModeMigrate, false, true}, {revive.ModeClone, false, true}, {revive.ModeMigrate, true, false}, {revive.ModeRollback, false, false}} {
		rp := &RevivePlan{RestoreSecret: tc.secrets}
		warnCredentialsReplaced(rp, &revive.Plan{Mode: tc.mode}, facts, prof)
		got := strings.Contains(strings.Join(rp.Warnings, "\n"), "这个备份不含凭据")
		if got != tc.warn {
			t.Fatalf("%s secrets=%v: %v", tc.mode, tc.secrets, rp.Warnings)
		}
	}
}

func gitBodyFixture(commit string) (*vault.Snapshot, profile.Profile) {
	head := &vault.Snapshot{Tier: vault.TierFull, Provenance: enroll.Provenance{Git: &enroll.GitRecord{Remote: "r", Commit: commit}}}
	return head, profile.Profile{ID: "x", DisplayName: "X"}
}

func blockerCodes(rp *RevivePlan) []string {
	var out []string
	for _, b := range rp.Blockers {
		out = append(out, b.Code)
	}
	return out
}

func TestGitBodyDecoupledPlan(t *testing.T) {
	head, prof := gitBodyFixture("aaaa")
	same := revive.InspectResult{Provenance: enroll.Provenance{Git: &enroll.GitRecord{Remote: "r", Commit: "aaaa"}}}
	other := revive.InspectResult{OS: "linux", Arch: "arm64", Provenance: enroll.Provenance{Git: &enroll.GitRecord{Remote: "r", Commit: "bbbb"}}}
	none := revive.InspectResult{OS: "linux", Arch: "amd64"}
	run := func(facts revive.InspectResult, o ReviveOptions) (*RevivePlan, revive.Plan) {
		rp := &RevivePlan{}
		plan := revive.Plan{Mode: o.Mode}
		planBodyOpts(rp, &plan, head, facts, prof, o)
		return rp, plan
	}
	// Same commit works for migrate and clone, keeping the target body.
	for _, m := range []string{revive.ModeMigrate, revive.ModeClone} {
		rp, plan := run(same, ReviveOptions{Mode: m})
		if len(rp.Blockers) != 0 || plan.Body.Action != revive.BodyKeep {
			t.Fatalf("%s same: %+v %+v", m, rp.Blockers, plan.Body)
		}
	}
	if rp, plan := run(same, ReviveOptions{Mode: revive.ModeRollback}); len(rp.Blockers) != 0 || plan.Body.Action != revive.BodyMoveDirs {
		t.Fatalf("rollback same: %+v", plan.Body)
	}
	// Different body on target: blocker, then opt-in keeps the target body.
	rp, _ := run(other, ReviveOptions{Mode: revive.ModeMigrate})
	if c := blockerCodes(rp); len(c) != 1 || c[0] != "body_version_mismatch" || !strings.Contains(rp.Blockers[0].Reason, "bbbb") || rp.BodySummary == "" {
		t.Fatalf("mismatch: %+v", rp.Blockers)
	}
	rp, plan := run(other, ReviveOptions{Mode: revive.ModeMigrate, AllowVersionMismatch: true})
	if len(rp.Blockers) != 0 || plan.Body.Action != revive.BodyKeep || rp.DataOnly || len(rp.Warnings) == 0 {
		t.Fatalf("allowed mismatch: %+v %+v", rp, plan.Body)
	}
	// A binary without recorded provenance also counts as installed.
	if rp, _ := run(revive.InspectResult{BinaryFound: true}, ReviveOptions{Mode: revive.ModeMigrate}); len(rp.Blockers) != 1 || rp.Blockers[0].Code != "body_version_mismatch" {
		t.Fatalf("binary: %+v", rp.Blockers)
	}
	// No harness on the target: blocker with both ways out, then data only.
	rp, _ = run(none, ReviveOptions{Mode: revive.ModeMigrate})
	if c := blockerCodes(rp); len(c) != 1 || c[0] != "body_rebuild_unverified" || !strings.Contains(rp.Blockers[0].Suggestion, "仅迁移数据与档案") {
		t.Fatalf("none: %+v", rp.Blockers)
	}
	rp, plan = run(none, ReviveOptions{Mode: revive.ModeMigrate, AllowVersionMismatch: true})
	if len(rp.Blockers) != 0 || !rp.DataOnly || plan.Body.Action != revive.BodyKeep || len(plan.Body.Dirs) != 0 {
		t.Fatalf("data only: %+v %+v", rp, plan.Body)
	}
}

func TestUninstalledTargetEmptyProvenanceStruct(t *testing.T) {
	head, prof := gitBodyFixture("aaaa11112222")
	// Real porter returns non-nil empty provenance structs when harness is not installed.
	emptyCases := []enroll.Provenance{
		{Kind: profile.ProvenanceGit, Git: &enroll.GitRecord{Path: "/home/u/.x/repo"}},
		{Kind: profile.ProvenanceNPMGlobal, NPM: &enroll.NPMRecord{Package: "demo-pkg"}},
		{Kind: profile.ProvenanceUVTool, Tool: &enroll.ToolRecord{Package: "demo-tool"}},
		{Kind: profile.ProvenanceBinary, Binary: &enroll.BinaryRecord{}},
	}
	for _, pv := range emptyCases {
		if hasProvenance(pv) {
			t.Fatalf("empty provenance struct falsely treated as installed: %+v", pv)
		}
		if got := describeBody(pv, "linux/amd64"); got != "未能识别版本的程序（linux/amd64）" {
			t.Fatalf("empty provenance rendered bad body description: %q", got)
		}
	}

	uninstalled := revive.InspectResult{
		OS:          "linux",
		Arch:        "amd64",
		BinaryFound: false,
		Provenance:  enroll.Provenance{Kind: profile.ProvenanceGit, Git: &enroll.GitRecord{Path: "/home/u/.x/repo"}},
	}
	rp := &RevivePlan{}
	plan := revive.Plan{Mode: revive.ModeMigrate}
	planBodyOpts(rp, &plan, head, uninstalled, prof, ReviveOptions{Mode: revive.ModeMigrate})
	if c := blockerCodes(rp); len(c) != 1 || c[0] != "body_rebuild_unverified" {
		t.Fatalf("expected body_rebuild_unverified for uninstalled target, got blockers=%+v summary=%q", rp.Blockers, rp.BodySummary)
	}

	// With AllowVersionMismatch: DataOnly=true and plan.Record.Provenance must NOT retain stale source Commit.
	rp = &RevivePlan{}
	plan = revive.Plan{Mode: revive.ModeMigrate}
	opts := ReviveOptions{Mode: revive.ModeMigrate, AllowVersionMismatch: true}
	planBodyOpts(rp, &plan, head, uninstalled, prof, opts)
	if len(rp.Blockers) != 0 || !rp.DataOnly || plan.Body.Action != revive.BodyKeep {
		t.Fatalf("expected data-only migrate on uninstalled target: rp=%+v body=%+v", rp, plan.Body)
	}
	rec := planRecord("demo", prof, []revive.RootPlan{{ID: profile.StateRoot, Path: "/home/u/.x"}}, head, uninstalled, rp, plan, opts)
	if rec.Provenance.Git != nil && rec.Provenance.Git.Commit != "" {
		t.Fatalf("plan.Record.Provenance retained stale source commit: %+v", rec.Provenance.Git)
	}
}

func TestPlanServiceDataOnlyAndMissing(t *testing.T) {
	prof := profile.Profile{ID: "x", Service: &profile.Service{Candidates: []profile.ServiceUnit{{Unit: "x.service"}}}}
	rp := &RevivePlan{DataOnly: true}
	plan := revive.Plan{}
	planService(rp, &plan, revive.InspectResult{}, prof, nil)
	if plan.Unit != "" || plan.InstallService || len(rp.Blockers) != 0 || len(rp.Warnings) != 1 {
		t.Fatalf("data only: %+v %+v", rp, plan)
	}
	rp, plan = &RevivePlan{}, revive.Plan{}
	planService(rp, &plan, revive.InspectResult{}, prof, nil)
	if len(rp.Blockers) != 1 || rp.Blockers[0].Code != "service_missing" || !strings.Contains(rp.Blockers[0].Suggestion, "仅迁移数据与档案") || strings.Contains(rp.Blockers[0].Suggestion, "--data-only") {
		t.Fatalf("missing: %+v", rp.Blockers)
	}
}

func TestPlanReviveServicePlanning(t *testing.T) {
	// (a) Stopped loaded unit (LoadState=loaded, Active=inactive): no service_missing,
	// sets plan.Unit and plan.InstallService == false.
	profLoaded := profile.Profile{
		ID:      "x",
		Service: &profile.Service{Candidates: []profile.ServiceUnit{{Scope: "user", Unit: "x.service"}}},
	}
	rp := &RevivePlan{}
	plan := revive.Plan{Profile: profLoaded}
	factsLoaded := revive.InspectResult{
		Service: []enroll.ServiceState{{Scope: "user", Unit: "x.service", LoadState: "loaded", Active: "inactive"}},
	}
	planService(rp, &plan, factsLoaded, profLoaded, nil)
	if len(rp.Blockers) != 0 || plan.Unit != "x.service" || plan.InstallService {
		t.Fatalf("stopped loaded unit: blockers=%+v unit=%q install=%v", rp.Blockers, plan.Unit, plan.InstallService)
	}

	// (b) Unloaded unit with older snapshot profile lacking install_argv while client
	// has newer effective profile declaring install_argv & bin_dirs for the same unit:
	// inherits InstallArgv + BinDirs, sets plan.InstallService=true, no service_missing,
	// does NOT force DataOnly, and does not mutate clientLP or original prof pointers.
	oldSvc := &profile.Service{Candidates: []profile.ServiceUnit{{Scope: "user", Unit: "x.service"}}}
	oldDetect := &profile.Detect{BinDirs: []string{"${HOME}/.old-bin"}}
	oldProf := profile.Profile{ID: "x", ProfileVersion: 1, Detect: oldDetect, Service: oldSvc}
	clientEffective := &profile.Profile{
		ID:             "x",
		ProfileVersion: 2,
		Detect:         &profile.Detect{BinDirs: []string{"${HOME}/.local/bin", "${HOME}/.old-bin"}},
		Service: &profile.Service{
			Candidates:  []profile.ServiceUnit{{Scope: "user", Unit: "x.service"}},
			InstallArgv: profile.Argv{"x-cli", "gateway", "install"},
		},
	}
	clientLP := &profiles.Loaded{Effective: clientEffective}
	rp = &RevivePlan{}
	plan = revive.Plan{Profile: oldProf}
	factsUnloaded := revive.InspectResult{
		Service: []enroll.ServiceState{{Scope: "user", Unit: "x.service", LoadState: "not-found", Active: "inactive"}},
	}
	planService(rp, &plan, factsUnloaded, oldProf, clientLP)
	if len(rp.Blockers) != 0 || !plan.InstallService || rp.DataOnly {
		t.Fatalf("inherited install_argv: blockers=%+v install=%v dataOnly=%v", rp.Blockers, plan.InstallService, rp.DataOnly)
	}
	if !slices.Equal(rp.InstallArgv, []string{"x-cli", "gateway", "install"}) {
		t.Fatalf("unexpected rp.InstallArgv: %v", rp.InstallArgv)
	}
	if plan.Profile.Service == nil || !slices.Equal([]string(plan.Profile.Service.InstallArgv), []string{"x-cli", "gateway", "install"}) {
		t.Fatalf("unexpected plan.Profile.Service: %+v", plan.Profile.Service)
	}
	if plan.Profile.Detect == nil || !slices.Contains(plan.Profile.Detect.BinDirs, "${HOME}/.local/bin") {
		t.Fatalf("expected merged bin_dirs on plan.Profile.Detect, got %+v", plan.Profile.Detect)
	}
	if !strings.Contains(strings.Join(rp.Warnings, "\n"), "x-cli gateway install") {
		t.Fatalf("expected calm warning mentioning install command, got %v", rp.Warnings)
	}
	// Verify shared pointers were NOT mutated.
	if len(oldSvc.InstallArgv) != 0 || len(oldDetect.BinDirs) != 1 {
		t.Fatalf("original snapshot profile pointers mutated: svc=%+v detect=%+v", oldSvc, oldDetect)
	}
	plan.Profile.Service.InstallArgv[0] = "mutated"
	if clientEffective.Service.InstallArgv[0] != "x-cli" {
		t.Fatalf("clientLP.Effective mutated via shared slice: %+v", clientEffective.Service.InstallArgv)
	}

	// (c) Unloaded unit with NO install_argv anywhere and stale local override shadowing newer official pack.
	rp = &RevivePlan{
		ProfileOverrideStale:    true,
		EffectiveProfileVersion: 1,
		OfficialProfileVersion:  2,
	}
	plan = revive.Plan{Profile: oldProf}
	staleLP := &profiles.Loaded{
		Local:             true,
		OverridesOfficial: true,
		Effective:         &profile.Profile{ID: "x", ProfileVersion: 1, Service: oldSvc},
		Official:          &profile.Profile{ID: "x", ProfileVersion: 2, Service: clientEffective.Service},
	}
	planService(rp, &plan, revive.InspectResult{}, oldProf, staleLP)
	if len(rp.Blockers) != 1 || rp.Blockers[0].Code != "service_missing" {
		t.Fatalf("expected service_missing blocker, got %+v", rp.Blockers)
	}
	if !strings.Contains(rp.Blockers[0].Suggestion, "官方资料") {
		t.Fatalf("expected stale override hint in suggestion, got %q", rp.Blockers[0].Suggestion)
	}
}

func TestDossierSummary(t *testing.T) {
	head := &vault.Snapshot{LocalOverrides: []profile.Override{{}}, ExternalRefs: []enroll.ExternalRef{{Path: "/opt/x", Kind: "external_dir"}}}
	rp := &RevivePlan{}
	rp.summarizeDossier(head)
	if rp.Dossier == nil || rp.Dossier.LocalOverrides != 1 || len(rp.Dossier.ExternalRefs) != 1 || !strings.Contains(strings.Join(rp.Warnings, ""), "/opt/x") {
		t.Fatalf("%+v %v", rp.Dossier, rp.Warnings)
	}
	rp = &RevivePlan{}
	rp.summarizeDossier(&vault.Snapshot{})
	if rp.Dossier != nil || len(rp.Warnings) != 0 {
		t.Fatal("empty dossier shown")
	}
}
