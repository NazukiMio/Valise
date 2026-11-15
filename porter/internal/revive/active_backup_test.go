package revive

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/activeview"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	"valise/porter/internal/seal"
)

func backupProgramme(t *testing.T, version string) (environments.ProgramMaterial, []byte) {
	t.Helper()
	program := []byte(version)
	files := []environments.ProgramFile{
		{Path: ".demo/program/agent", Size: int64(len(program)), SHA256: vault.SHA256Hex(program), Mode: 0700},
		{Path: ".demo/program/current", SHA256: vault.SHA256Hex([]byte("agent")), Mode: 0777, LinkTarget: "agent"},
	}
	var data bytes.Buffer
	tw := tar.NewWriter(&data)
	for _, f := range files {
		h := &tar.Header{Name: f.Path, Mode: int64(f.Mode), Size: f.Size, Typeflag: tar.TypeReg}
		if f.LinkTarget != "" {
			h.Typeflag, h.Linkname = tar.TypeSymlink, f.LinkTarget
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if f.LinkTarget == "" {
			if _, err := tw.Write(program); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return environments.ProgramMaterial{SourceDigest: version, ArchiveSHA: vault.SHA256Hex(data.Bytes()), ArchiveBytes: int64(data.Len()), OS: runtime.GOOS, Arch: runtime.GOARCH, Files: files}, data.Bytes()
}

func activeBackupFixture(t *testing.T) (*managedWorld, *activationrecord.Record, *age.X25519Identity, *environments.ProgramBinding) {
	t.Helper()
	ctx := context.Background()
	w := newWorld(t)
	canonical, err := filepath.EvalSymlinks(w.home)
	if err != nil {
		t.Fatal(err)
	}
	w.home, w.state, w.bin = canonical, filepath.Join(canonical, ".demo"), filepath.Join(canonical, "bin")
	w.r.Store.Home, w.r.Env.Home = canonical, canonical
	if err := os.Chmod(w.state, 0700); err != nil {
		t.Fatal(err)
	}
	w.p.Schema, w.p.DisplayName, w.p.Status, w.p.Consistency = profile.Schema, "Fixture", profile.StatusDraft, nil
	w.p.Provenance = &profile.Provenance{Kind: profile.ProvenanceBinary, Binary: &profile.BinaryProvenance{Name: "fixture"}}
	w.p.Detect = &profile.Detect{StateDirCandidates: []string{"${HOME}/.demo"}}
	w.p.Body = &profile.Body{Paths: []profile.PathRef{{Root: "state", Glob: "program/**"}}}
	w.p.Entries = []profile.Entry{{ID: "gateway", Kind: "web", Host: "127.0.0.1", Port: 18391, Path: "/"}}
	w.p.Health = &profile.Health{Checks: []profile.HealthCheck{{Kind: "http", Entry: "gateway", ExpectStatus: []int{200}}}}
	w.p.Environments = &profile.Environments{Engine: "linux-bwrap", StartArgv: profile.Argv{"${STATE_DIR}/program/agent"}, WorkingDir: "${STATE_DIR}",
		Path: []string{"${HOME}/.demo/program"}, ProgramPaths: []profile.PathRef{{Root: "state", Glob: "program"}}, GatewayEntry: "gateway", Verification: profile.Capability{Status: "untested"}}
	oldOrigin := enroll.Provenance{Kind: profile.ProvenanceBinary, Binary: &enroll.BinaryRecord{Path: w.state + "/program/agent", SHA256: vault.SHA256Hex([]byte("version-A"))}}
	_, agent, err := porterenroll.AcceptRecord(w.home, enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: w.p.ProfileVersion,
		Roots: map[string]string{"state": w.state}, Provenance: oldOrigin, EnrolledAt: time.Now().UTC()}, w.p)
	if err != nil {
		t.Fatal(err)
	}
	layout := environments.Layout{Home: "/source/home", Roots: map[string]string{"state": "/source/home/.demo"}}
	runtimeRecord := func(version string) *vault.RuntimeRecord {
		origin := oldOrigin
		origin.Binary = &enroll.BinaryRecord{Path: layout.Roots["state"] + "/program/agent", SHA256: vault.SHA256Hex([]byte(version))}
		return &vault.RuntimeRecord{Format: vault.FormatRuntime, CapturedAt: time.Now().UTC(), OS: runtime.GOOS, Arch: runtime.GOARCH,
			Home: vault.RuntimeRoot{Path: layout.Home, Resolved: layout.Home}, Roots: map[string]vault.RuntimeRoot{"state": {Path: layout.Roots["state"], Resolved: layout.Roots["state"]}},
			Installation: &vault.RuntimeInstallation{InstallID: "demo", DefaultInstanceID: "demo", ProfileDoc: &w.p, RegisteredProvenance: origin},
			Dependencies: []vault.RuntimeDependency{{Role: "program", Path: layout.Roots["state"] + "/program/agent", SHA256: origin.Binary.SHA256}}}
	}
	programs := environments.ProgramStore{Home: w.home}
	materialA, archiveA := backupProgramme(t, "version-A")
	_, a, err := programs.BuildOriginal(ctx, agent.Ref, environments.OriginalProgramInput{Profile: w.p, Layout: layout, Program: materialA, Runtime: runtimeRecord("version-A"), Archive: bytes.NewReader(archiveA), MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	store := environments.Store{Home: w.home}
	reg, err := store.Update(agent.Ref, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(agent.Ref) })
	if err != nil {
		t.Fatal(err)
	}
	snapshot := identity.SnapshotRef{VaultID: vault.NewID("v", time.Now()), SnapshotID: vault.NewID("s", time.Now())}
	reg, err = store.Update(agent.Ref, reg.Revision, func(r environment.Registry) (environment.Registry, error) {
		return r.Begin(strings.Repeat("a", 32), snapshot)
	})
	if err != nil {
		t.Fatal(err)
	}
	materialB, archiveB := backupProgramme(t, "version-B")
	source := environment.Source{Agent: agent.Ref, Snapshot: snapshot, Time: time.Now().UTC(), Tier: vault.TierFull, ProfileDigest: environments.Digest(w.p), LayoutDigest: environments.LayoutDigest(layout, nil), ProgramDigest: materialB.SourceDigest, Verified: true}
	file := vault.File{Root: "state", Path: "SOUL.md", Category: "identity", Size: 8, SHA256: vault.SHA256Hex([]byte("restored")), Mode: 0600}
	_, b, err := programs.BuildBound(ctx, agent.Ref, reg.Operation.Candidate, environments.BuildInput{Profile: w.p, Layout: layout, Program: materialB, Runtime: runtimeRecord("version-B"), Archive: bytes.NewReader(archiveB), MaxBytes: 1 << 20,
		Target: environment.Material{Source: source, Files: vault.Files{Format: vault.FormatFiles, Files: []vault.File{file}}},
		OpenData: func(context.Context, identity.SnapshotRef, string, string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("restored")), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	p := w.plan()
	p.SnapshotID, p.Tier, p.Carry, p.Unit, p.Body = snapshot.SnapshotID, vault.TierFull, false, "", revive.BodyPlan{Action: revive.BodyKeep}
	m := &managedWorld{world: w, p: p, scope: revive.ManagedDataRef{Agent: agent.Ref, OperationID: reg.Operation.ID, Snapshot: snapshot, Original: reg.Operation.Original, Candidate: reg.Operation.Candidate},
		manifest: []revive.ManifestEntry{{Path: "state/SOUL.md", Size: 8, SHA256: file.SHA256, Category: "identity"}}}
	m.begin(t)
	j, err := w.r.Store.Load(p.OpID)
	if err != nil {
		t.Fatal(err)
	}
	parent := &activationrecord.Record{Schema: activationrecord.Schema,
		Plan: activationrecord.Plan{Agent: agent.Ref, OperationID: reg.Operation.ID, Snapshot: snapshot, Original: m.scope.Original, Candidate: m.scope.Candidate, DataOpID: p.OpID,
			OriginalMaterials: environments.Digest(a), CandidateMaterials: environments.Digest(b), OriginalProfile: w.p, CandidateProfile: w.p},
		OriginalDefinition: strings.Repeat("b", 64), CandidateDefinition: strings.Repeat("c", 64), DataPlanDigest: j.ManagedData.PlanDigest, DataManifestDigest: j.ManagedData.ManifestDigest, State: "ready"}
	parent.BindingDigest = activationrecord.Digest(parent)
	if err := activationrecord.Save(w.home, parent); err != nil {
		t.Fatal(err)
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := seal.AddRecipient(seal.Paths{Home: w.home}, id.Recipient().String(), "fixture"); err != nil {
		t.Fatal(err)
	}
	return m, parent, id, b
}

func sealActiveFixture(t *testing.T, m *managedWorld, id *age.X25519Identity, tier string) (*vault.Snapshot, map[string]string) {
	t.Helper()
	p := seal.Paths{Home: m.home}
	rep, err := seal.Seal(context.Background(), m.r.Env, p, vaultrpc.SealParams{InstanceID: "demo", Tier: tier, Reason: "fixture"}, vaultrpc.InitiatorClient)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := p.LeafDir(rep.Leaf)
	if err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, files, err := v.Snapshot(rep.SnapshotID, id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files.Files {
		var data bytes.Buffer
		if err := v.WriteFile(f, &data, id); err != nil {
			t.Fatal(err)
		}
		out[f.Root+":"+f.Path] = data.String()
	}
	for _, link := range files.Symlinks {
		out["link "+link.Root+":"+link.Path] = link.Target
	}
	return snapshot, out
}

func TestDailyBackupFollowsActiveDataAndIndependentProgrammeWithoutService(t *testing.T) {
	m, parent, id, b := activeBackupFixture(t)
	ctx := context.Background()
	m.transition(t, environment.Registry.Prepared)
	// Leftover legacy metadata must not be auto-imported by backup or nested
	// instance discovery and thereby replace this managed Agent's profile.
	legacyProfile := m.world.p
	legacyProfile.ProfileVersion++
	legacyRecord := enroll.Record{InstanceID: "demo-legacy", ProfileID: "demo", ProfileVersion: legacyProfile.ProfileVersion, Name: "legacy", Roots: map[string]string{"state": filepath.Join(m.home, ".legacy")}}
	legacy, _ := json.Marshal(legacyRecord)
	legacyDoc, _ := json.Marshal(legacyProfile)
	legacyPath := filepath.Join(porterenroll.InstancesDir(m.home), "demo-legacy.json")
	write(t, legacyPath, string(legacy), 0600)
	write(t, filepath.Join(porterenroll.InstancesDir(m.home), "demo-legacy.profile.json"), string(legacyDoc), 0600)
	a, files := sealActiveFixture(t, m, id, vault.TierSoul)
	if read(t, legacyPath) != string(legacy) {
		t.Fatal("daily backup migrated unrelated legacy metadata")
	}
	// The exchange controller's legacy migration compatibility is outside this
	// read-only consumer test; retain these fixtures under inert filenames.
	if err := os.Rename(legacyPath, legacyPath+".retained"); err != nil {
		t.Fatal(err)
	}
	if files["state:SOUL.md"] != "current soul" || a.Provenance.Binary == nil || a.Provenance.Binary.SHA256 != vault.SHA256Hex([]byte("version-A")) {
		t.Fatal("prepared B replaced active A backup source")
	}
	m.transition(t, environment.Registry.StopOriginal)
	if _, err := seal.Seal(ctx, m.r.Env, seal.Paths{Home: m.home}, vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierFull}, vaultrpc.InitiatorClient); err == nil {
		t.Fatal("transition produced mixed backup")
	}
	m.transition(t, environment.Registry.OriginalStopped)
	if _, err := m.r.ExchangeManagedData(ctx, m.p.OpID); err != nil {
		t.Fatal(err)
	}
	m.transition(t, environment.Registry.StartCandidate)
	m.transition(t, environment.Registry.GatewayPassed)
	parent.State = "awaiting_human"
	if err := activationrecord.Save(m.home, parent); err != nil {
		t.Fatal(err)
	}
	// These host bytes are hidden by the running programme mount. A raw
	// backup must include private B and its link, not stale or extra host code.
	write(t, filepath.Join(m.state, "program/agent"), "old host programme", 0700)
	write(t, filepath.Join(m.state, "program/deleted"), "hidden stale file", 0600)
	snap, files := sealActiveFixture(t, m, id, vault.TierRaw)
	if files["state:SOUL.md"] != "restored" || files["state:program/agent"] != "version-B" || files["link state:program/current"] != "agent" || files["state:program/deleted"] != "" || snap.Provenance.Binary.SHA256 != vault.SHA256Hex([]byte("version-B")) {
		t.Fatal("backup mixed original programme/data with active candidate", files)
	}
	_, files = sealActiveFixture(t, m, id, vault.TierFull)
	for path := range files {
		if strings.Contains(path, "program/") {
			t.Fatal("programme evidence expanded ordinary data tier", path)
		}
	}
	base, _ := (environments.ProgramStore{Home: m.home}).RevisionPath(m.scope.Agent, b.Ref)
	write(t, filepath.Join(base, b.Workspace, "programs/.demo/program/agent"), "damaged independent programme", 0700)
	broken, data := sealActiveFixture(t, m, id, vault.TierFull)
	if data["state:SOUL.md"] != "restored" || broken.Provenance.Kind != "" || len(broken.Runtime.Dependencies) != 0 || !slices.Contains(broken.Runtime.Unresolved, "active_program_unverified") {
		t.Fatal("broken programme prevented data backup or borrowed original source")
	}
	if _, err := seal.Seal(ctx, m.r.Env, seal.Paths{Home: m.home}, vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierRaw}, vaultrpc.InitiatorClient); err == nil {
		t.Fatal("raw accepted changed independent programme")
	}
	// Data binding does not need a service query or programme verification.
	if current, err := (activeview.Reader{Home: m.home}).ReadData(ctx, m.scope.Agent); err != nil || current.Ref != b.Ref {
		t.Fatal("broken programme invalidated active data identity", current, err)
	}
	// A missing registry cannot silently reset the Agent to its old native
	// source while the original formal preparation still owns these roots.
	path := filepath.Join(m.home, ".local/state/valise/environments", environment.Key(m.scope.Agent)+".json")
	// This synthetic fixture builds the parent directly, so preserve an owned
	// registry under the same instance with a different generation as well.
	var owner environment.Registry
	store := environments.Store{Home: m.home}
	owner, err := store.Load(m.scope.Agent)
	if err != nil {
		t.Fatal(err)
	}
	owner.Agent.Generation = "another-generation"
	if _, err := store.Update(owner.Agent, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(owner.Agent) }); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".missing"); err != nil {
		t.Fatal(err)
	}
	if _, err := seal.Seal(ctx, m.r.Env, seal.Paths{Home: m.home}, vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierFull}, vaultrpc.InitiatorClient); err == nil {
		t.Fatal("missing current registry fell back past existing environment ownership")
	}
}
