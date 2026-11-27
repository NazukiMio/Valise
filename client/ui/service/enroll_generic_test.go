package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/client/profiles"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/vault"
	"valise/testbed/fakesshenv"
)

func TestEnrollGenericPreviewAcceptAndNoSecretsGuard(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")

	// Seed an unlisted custom harness state directory on the server.
	stateDir := filepath.Join(home, ".my-custom-agent")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "config.json"), []byte(`{"token":"raw"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "memory.md"), []byte("# memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newService(t, e.SSH)
	ctx := context.Background()
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}

	in := profiles.GenericProfileInput{
		DisplayName: "Custom Agent",
		StateDir:    "~/.my-custom-agent",
	}

	// Missing directory on server returns a clear UserError without writing any local YAML.
	_, err := s.EnrollGeneric(ctx, "box", profiles.GenericProfileInput{
		DisplayName: "Missing",
		StateDir:    "~/.does-not-exist",
	}, true)
	wantCode(t, err, "generic_state_dir_missing")
	localYAML := filepath.Join(profiles.LocalDir(s.Dirs.Data), "custom-missing.yaml")
	if _, statErr := os.Stat(localYAML); !os.IsNotExist(statErr) {
		t.Fatalf("missing dir should not leave local profile YAML behind")
	}

	// Preview (accept=false) must NOT write local profile YAML or dossier on server.
	prev, err := s.EnrollGeneric(ctx, "box", in, false)
	if err != nil {
		t.Fatalf("EnrollGeneric preview failed: %v", err)
	}
	if !prev.ProfileLocal || prev.Written != "" || prev.Report == nil || prev.Report.Unclassified.Files != 2 {
		t.Fatalf("unexpected preview: %+v", prev)
	}
	customYAML := filepath.Join(profiles.LocalDir(s.Dirs.Data), "custom-custom-agent.yaml")
	if _, statErr := os.Stat(customYAML); !os.IsNotExist(statErr) {
		t.Fatalf("preview must not write %s", customYAML)
	}
	if !strings.Contains(strings.Join(prev.Report.Warnings, "\n"), "凭据白名单") {
		t.Fatalf("preview missing no-secrets warning: %+v", prev.Report.Warnings)
	}

	// Official profile ID collision must be rejected.
	// Note enrollWorld sets up local demo.yaml, so check with official-like conflict via non-local is tested in profiles;
	// here confirm accept=true writes YAML + dossier and refreshes Instances with HasSecretRules=false.
	acc, err := s.EnrollGeneric(ctx, "box", in, true)
	if err != nil {
		t.Fatalf("EnrollGeneric accept failed: %v", err)
	}
	if acc.Written == "" {
		t.Fatalf("expected Written dossier path: %+v", acc)
	}
	if _, statErr := os.Stat(customYAML); statErr != nil {
		t.Fatalf("accepted generic enroll missing local profile YAML: %v", statErr)
	}

	insts, err := s.Instances(ctx, "box")
	if err != nil || len(insts) != 1 {
		t.Fatalf("Instances = %+v err=%v", insts, err)
	}
	if insts[0].HasSecretRules == nil || *insts[0].HasSecretRules {
		t.Fatalf("expected HasSecretRules=false for generic instance, got %+v", insts[0].HasSecretRules)
	}

	// Guardrail: TierSoul ("soul-only" / TierSoul) must be rejected on both manual savepoint and protection plan.
	_, err = s.CreateManualSavepoint(ctx, ManualSavepointRequest{
		Host:       "box",
		InstanceID: insts[0].InstanceID,
		Tier:       "soul-only",
		Note:       "should-block",
	})
	wantCode(t, err, "no_secret_rules_declared")

	var b identity.Binding
	if err := s.call(ctx, "box", identity.MethodGet, identity.GetParams{InstanceID: insts[0].InstanceID}, &b); err != nil {
		t.Fatal(err)
	}
	desired := protection.NewDefault()
	desired.Scope.Tier = vault.TierSoul
	_, err = s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{
		AgentRef: b.Agent.Ref,
		Desired:  desired,
	})
	wantCode(t, err, "no_secret_rules_declared")

	// Add a LocalEntry via UpdateDossier (owner RPC), verify LoginMethods + ProbeEntry pick it up, then remove via RemoveLocalEntry.
	if _, err := s.UpdateDossier(ctx, "box", enroll.DossierUpdateParams{
		InstanceID: insts[0].InstanceID,
		AddEntries: []enroll.LocalEntry{{
			ID: "webui", DisplayName: "Custom WebUI", Port: 7861, Path: "/app", Hint: "Start via myagent web",
		}},
	}); err != nil {
		t.Fatalf("UpdateDossier AddEntries failed: %v", err)
	}
	lt, err := s.LoginMethods(ctx, "box", insts[0].InstanceID)
	if err != nil || len(lt.Profile.Entries) != 1 || lt.Profile.Entries[0].ID != "webui" {
		t.Fatalf("LoginMethods missing LocalEntry: %+v err=%v", lt, err)
	}
	probe, err := s.ProbeEntry(ctx, "box", insts[0].InstanceID, "webui")
	if err != nil || probe.AgentHint != "Start via myagent web" || probe.LocalSource != "agent_dossier" {
		t.Fatalf("ProbeEntry = %+v err=%v", probe, err)
	}
	if err := s.RemoveLocalEntry(ctx, "box", insts[0].InstanceID, "webui"); err != nil {
		t.Fatalf("RemoveLocalEntry failed: %v", err)
	}
	ltAfter, err := s.LoginMethods(ctx, "box", insts[0].InstanceID)
	if err != nil || len(ltAfter.Profile.Entries) != 0 {
		t.Fatalf("expected LocalEntry removed, got %+v err=%v", ltAfter, err)
	}
}

func TestRevivePlanCarriesLocalEntries(t *testing.T) {
	rp := &RevivePlan{}
	head := &vault.Snapshot{
		InstanceID:   "demo",
		Profile:      vault.ProfileRef{ID: "demo", Version: 1},
		LocalEntries: []enroll.LocalEntry{{ID: "webapp", DisplayName: "WebApp", Port: 7860, Path: "/ui", Hint: "hint"}},
	}
	rp.summarizeDossier(head)
	if rp.Dossier == nil || len(rp.Dossier.LocalEntries) != 1 || rp.Dossier.LocalEntries[0].ID != "webapp" {
		t.Fatalf("summarizeDossier missing LocalEntries: %+v", rp.Dossier)
	}
	rec := planRecord("demo", profile.Profile{ID: "demo", ProfileVersion: 1},
		[]revive.RootPlan{{ID: profile.StateRoot, Path: "/home/u/.demo"}},
		head, revive.InspectResult{}, rp, revive.Plan{}, ReviveOptions{})
	if len(rec.LocalEntries) != 1 || rec.LocalEntries[0].ID != "webapp" {
		t.Fatalf("planRecord missing LocalEntries: %+v", rec.LocalEntries)
	}
}
