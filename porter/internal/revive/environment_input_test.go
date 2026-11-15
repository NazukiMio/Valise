package revive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/vault"
	"valise/porter/internal/backuprun"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
	"valise/porter/internal/isolated"
)

func environmentWorld(t *testing.T, executable ...[]byte) (*world, revive.Plan) {
	var program []byte
	if len(executable) > 0 {
		program = executable[0]
	}
	return environmentWorldProfile(t, program, nil)
}

func environmentWorldProfile(t *testing.T, executable []byte, change func(*profile.Profile)) (*world, revive.Plan) {
	return environmentWorldProfileRoots(t, executable, change, nil)
}

func environmentWorldProfileRoots(t *testing.T, executable []byte, change func(*profile.Profile), extra func(*world) map[string]string) (*world, revive.Plan) {
	t.Helper()
	w := newWorld(t)
	w.p.Schema, w.p.DisplayName, w.p.Status = profile.Schema, "Demo", profile.StatusDraft
	w.p.Provenance = &profile.Provenance{Kind: profile.ProvenanceNPMGlobal, NPM: &profile.NPMProvenance{Package: "fixture"}}
	w.p.Detect = &profile.Detect{StateDirCandidates: []string{"${HOME}/.demo"}}
	w.p.Body = &profile.Body{Paths: []profile.PathRef{{Root: "state", Glob: "tools/**"}, {Root: "state", Glob: "bin/**"}}}
	w.p.Paths = append(w.p.Paths, profile.PathRule{Root: "state", Glob: "tools/**", Category: "body"}, profile.PathRule{Root: "state", Glob: "bin/**", Category: "body"})
	w.p.Entries = []profile.Entry{{ID: "gateway", Kind: "web", Host: "127.0.0.1", Port: 18391, Path: "/"}}
	w.p.Health = &profile.Health{Checks: []profile.HealthCheck{{Kind: "http", Entry: "gateway", ExpectStatus: []int{200}}}}
	w.p.Environments = &profile.Environments{Engine: "linux-bwrap", StartArgv: profile.Argv{"${STATE_DIR}/bin/gateway"}, WorkingDir: "${STATE_DIR}",
		Path: []string{"${HOME}/.demo/bin"}, ProgramPaths: []profile.PathRef{{Root: "state", Glob: "bin"}, {Root: "state", Glob: "tools"}}, GatewayEntry: "gateway", Verification: profile.Capability{Status: "untested"}}
	if change != nil {
		change(&w.p)
	}
	if issues := profile.Lint([]*profile.Document{{Profile: &w.p}})[0].Issues; len(issues) != 0 {
		t.Fatal(issues)
	}
	prefix := filepath.Join(w.state, "tools", "npm")
	write(t, filepath.Join(prefix, "lib/node_modules/fixture/package.json"), `{"name":"fixture","version":"1.2.3"}`, 0600)
	program := "private program"
	if len(executable) > 0 {
		program = string(executable)
		w.p.Environments.StartArgv = append(w.p.Environments.StartArgv, "-test.run=TestEnvironmentGuest", "-environment-guest")
	}
	write(t, filepath.Join(w.state, "bin/gateway"), program, 0700)
	origin := enroll.Provenance{Kind: profile.ProvenanceNPMGlobal, NPM: &enroll.NPMRecord{Package: "fixture", Version: "1.2.3", Prefix: prefix,
		Registry: "https://registry.example.test/", Integrity: "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))}}
	roots := map[string]string{"state": w.state}
	if extra != nil {
		roots = extra(w)
	}
	for _, root := range roots {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: w.p.ProfileVersion, Roots: roots, Provenance: origin, EnrolledAt: time.Now().UTC()}
	_, agent, err := porterenroll.AcceptRecord(w.home, rec, w.p)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := environments.ObserveInstalledNPM(context.Background(), environments.InstalledSource{Profile: w.p, Layout: environments.Layout{Home: w.home, Roots: rec.Roots}, Origin: origin, MaxBytes: 256 << 20})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ref := identity.SnapshotRef{VaultID: vault.NewID("v", now), SnapshotID: vault.NewID("s", now)}
	runtimeRoots := map[string]vault.RuntimeRoot{}
	for id, root := range roots {
		runtimeRoots[id] = vault.RuntimeRoot{Path: root, Resolved: root}
	}
	runtimeRecord := &vault.RuntimeRecord{Format: vault.FormatRuntime, OS: runtime.GOOS, Arch: runtime.GOARCH, Home: vault.RuntimeRoot{Path: w.home, Resolved: w.home},
		Roots: runtimeRoots, Dependencies: observed.Dependencies,
		Installation: &vault.RuntimeInstallation{InstallID: "demo", DefaultInstanceID: "demo", ProfileDoc: &w.p, RegisteredProvenance: origin, Roots: runtimeRoots}}
	p := testPlan(w)
	p.Roots, err = revive.CanonicalRoots(roots)
	if err != nil {
		t.Fatal(err)
	}
	p.Tier, p.SnapshotID = vault.TierFull, ref.SnapshotID
	p.IsolatedTest = &environment.TestInput{VaultID: ref.VaultID, HeaderDigest: strings.Repeat("a", 64),
		Header: vault.Snapshot{Format: vault.FormatSnapshot, SnapshotID: ref.SnapshotID, InstanceID: "demo", Tier: p.Tier, CreatedAt: now,
			Profile: vault.ProfileRef{ID: "demo", Version: w.p.ProfileVersion}, ProfileDoc: &w.p, Provenance: origin, Runtime: runtimeRecord, Roots: rec.Roots},
		Files: vault.Files{Format: vault.FormatFiles, Files: []vault.File{{Root: "state", Path: "SOUL.md", Category: "identity", Size: 8, SHA256: vault.SHA256Hex([]byte("snapshot")), Mode: 0600}}}}
	request := protection.BackupParams{AgentRef: agent.Ref, RunID: strings.Repeat("a", 32), ExpectedRevision: 1, Reason: "manual"}
	digest, err := protection.Digest(request)
	if err != nil {
		t.Fatal(err)
	}
	desired := protection.NewDefault()
	desired.Schedule.DailyAt, desired.Schedule.Timezone = "03:00", "UTC"
	desired.Period = protection.Period{Unit: "month", Timezone: "UTC"}
	run := protection.BackupRun{Schema: protection.BackupRunSchema, RunID: request.RunID, AgentRef: agent.Ref, Request: request, RequestDigest: digest, State: "succeeded", Snapshot: &ref,
		Plan:       protection.ServerPlan{SchemaVersion: protection.Schema, AgentRef: agent.Ref, Revision: 1, Desired: desired, AcceptedAt: now},
		Components: []protection.BackupComponent{{Kind: "server_backup", State: "succeeded"}}, AcceptedAt: now, UpdatedAt: now}
	b, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(w.home, ".local/state/valise/runs", request.RunID+".json"), string(b), 0600)
	binding, err := (backuprun.Store{Home: w.home}).SnapshotBinding(context.Background(), protection.SnapshotBindingParams{SnapshotRef: ref})
	if err != nil || binding.State != "bound" {
		t.Fatalf("fixture binding %v %v", binding, err)
	}
	current, err := porteridentity.Read(w.home, "demo")
	if err != nil || current.Agent == nil || *binding.AgentRef != current.Agent.Ref {
		t.Fatalf("fixture identity %v %v", current, err)
	}
	return w, p
}

func TestEnvironmentInputUsesVerifiedTestStageAndAcceptedProfile(t *testing.T) {
	w, p := environmentWorld(t)
	w.stage(t, p, []snapFile{{"SOUL.md", "snapshot"}})
	j, _ := w.r.Store.Load(p.OpID)
	in, source, _, err := w.r.environmentTestInput(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := in.OpenData(context.Background(), in.Target.Source.Snapshot, "state", "SOUL.md")
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(b) != "snapshot" || read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("data reader borrowed active state", err)
	}
	archive, err := environments.FreezeInstalledNPM(context.Background(), t.TempDir(), source, p.IsolatedTest.Header.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.File.Close()
	j.Plan.Profile.Environments.StartArgv = profile.Argv{"${HOME}/arbitrary"}
	if _, _, _, err := w.r.environmentTestInput(context.Background(), j); err == nil {
		t.Fatal("request replaced accepted executable")
	}
	if !isolated.ValidHome(in.Layout.Home) {
		t.Skip("temporary HOME under a reserved isolation root; data and profile binding checked, private build has separate compatible-layout tests")
	}
	in.Program, in.Archive = archive.Material, archive.File
	in.Target.Source.LayoutDigest = environments.LayoutDigest(in.Layout, in.Overrides)
	c, err := environments.Build(context.Background(), t.TempDir(), in)
	if err != nil || !c.Receipt.CandidateBuilt {
		t.Fatal("verified transfer could not build private candidate", err)
	}
	if b := read(t, filepath.Join(c.Workspace.HomeDir(), ".demo/SOUL.md")); b != "snapshot" {
		t.Fatal("private candidate did not contain selected snapshot")
	}

}

func TestEnvironmentRunFailureIsDurableAndNeverReplayed(t *testing.T) {
	w, p := environmentWorld(t)
	w.stage(t, p, []snapFile{{"SOUL.md", "snapshot"}})
	write(t, filepath.Join(w.state, "bin/gateway"), "changed program", 0700)
	result, err := w.r.TestEnvironment(context.Background(), p.OpID)
	if err == nil || result == nil || result.State != "finished" || result.FailureCode != "environment_source_unavailable" || result.ProbeAttempted {
		t.Fatal("changed runtime was run", result, err)
	}
	// Even after repairing the original bytes the same operation cannot run.
	write(t, filepath.Join(w.state, "bin/gateway"), "private program", 0700)
	again, err := w.r.TestEnvironment(context.Background(), p.OpID)
	if err != nil || again.FailureCode != result.FailureCode || again.ProbeAttempted {
		t.Fatal("failed operation was replayed", again, err)
	}
	if _, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "discard"}); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("test changed active data")
	}
	if _, err := os.Lstat(filepath.Join(w.home, ".local/state/valise/environment-tests", p.OpID)); !os.IsNotExist(err) {
		t.Fatal("failed materials not removed")
	}
}
