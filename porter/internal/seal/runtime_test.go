package seal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

func TestNamedSnapshotRetainsInstallationOriginWithoutInheritingCapability(t *testing.T) {
	w := newWorld(t)
	rec, _ := porterenroll.ReadRecord(w.p.Home, "demo")
	p, _ := porterenroll.ReadProfile(w.p.Home, "demo")
	accepted := time.Unix(100, 0).UTC()
	rec.EnrolledAt = accepted
	rec.Provenance = enroll.Provenance{Kind: "git", Git: &enroll.GitRecord{Path: filepath.Join(w.state, "program"), Remote: "https://example.org/official.git", Commit: "registered-revision"}}
	p.Provenance = &profile.Provenance{Kind: "git", Git: &profile.GitProvenance{Root: "state", Path: "program"}}
	p.Environments = &profile.Environments{Engine: "linux-bwrap", Verification: profile.Capability{Status: "tested", Versions: []string{"fixture-version"}, Env: "fixture", Date: "2026-11-12"}}
	if _, err := porterenroll.WriteRecord(w.p.Home, *rec, *p); err != nil {
		t.Fatal(err)
	}
	physical := filepath.Join(w.p.Home, "named-physical")
	logical := filepath.Join(w.p.Home, "named-source")
	write(t, filepath.Join(physical, "SOUL.md"), "named memory")
	write(t, filepath.Join(physical, ".env"), "named-credential-never-read")
	if err := os.Symlink(physical, logical); err != nil {
		t.Fatal(err)
	}
	named := enroll.Record{InstanceID: "demo-bot", ProfileID: p.ID, ProfileVersion: p.ProfileVersion, Name: "bot", Roots: map[string]string{"state": logical}, EnrolledAt: accepted.Add(time.Second)}
	if _, err := porterenroll.WriteRecord(w.p.Home, named, *p); err != nil {
		t.Fatal(err)
	}
	dossier := porterenroll.DossierPath(w.p.Home, "demo")
	before, _ := os.ReadFile(dossier)
	rep, err := Seal(context.Background(), w.env, w.p, vaultrpc.SealParams{InstanceID: named.InstanceID, Tier: vault.TierSoul}, vaultrpc.InitiatorClient)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := w.p.LeafDir(rep.Leaf)
	if err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := v.SnapshotHeader(rep.SnapshotID, w.id)
	if err != nil || snap.Format != vault.FormatSnapshot || snap.Runtime == nil || snap.Runtime.Installation == nil {
		t.Fatal("runtime source was not sealed", err)
	}
	r := snap.Runtime
	installation := r.Installation
	canonical, err := filepath.EvalSymlinks(physical)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Provenance.Kind != "" || snap.ProfileDoc.Environments != nil || !installation.ProfileDoc.EnvironmentTested("fixture-version") {
		t.Fatal("named Agent inherited default capabilities or provenance")
	}
	if installation.InstallID != "demo" || installation.DefaultInstanceID != "demo" || !installation.AcceptedAt.Equal(accepted) || installation.RegisteredProvenance.Git.Commit != "registered-revision" {
		t.Fatal("registered origin was lost or replaced by a present-day guess")
	}
	if r.Home.Path != w.p.Home || r.Roots["state"].Path != logical || r.Roots["state"].Resolved != canonical || r.OS == "" || r.Arch == "" || r.CapturedAt.IsZero() {
		t.Fatal("source layout facts lost")
	}
	if !slices.Contains(r.Unresolved, "runtime_dependencies_unverified") || len(r.Dependencies) != 0 {
		t.Fatal("registration was promoted to verified runtime")
	}
	encoded, _ := json.Marshal(r)
	if bytes.Contains(encoded, []byte("named-credential-never-read")) {
		t.Fatal("credential contents were inspected")
	}
	after, _ := os.ReadFile(dossier)
	if !bytes.Equal(before, after) {
		t.Fatal("runtime capture rewrote registration")
	}
	meta, _ := os.ReadFile(filepath.Join(dir, vault.MetaPath(rep.SnapshotID)))
	if bytes.Contains(meta, []byte(w.p.Home)) || bytes.Contains(meta, []byte("runtime")) || bytes.Contains(meta, []byte("registered-revision")) {
		t.Fatal("runtime source leaked into plaintext metadata")
	}
}

func TestRuntimeCaptureDoesNotInventMissingDefaultOrLayout(t *testing.T) {
	w := newWorld(t)
	rec, _ := porterenroll.ReadRecord(w.p.Home, "demo")
	rec.Roots["state"] = filepath.Join(w.p.Home, "missing")
	r, err := captureRuntime(context.Background(), w.p.Home, rec, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Roots["state"].Resolved != "" || !slices.Contains(r.Unresolved, "layout_unresolved") {
		t.Fatal("missing source layout was guessed")
	}
	p, _ := porterenroll.ReadProfile(w.p.Home, "demo")
	named := enroll.Record{InstanceID: "demo-bot", ProfileID: p.ID, ProfileVersion: p.ProfileVersion, Name: "bot", Roots: map[string]string{"state": w.state}}
	if _, err := porterenroll.WriteRecord(w.p.Home, named, *p); err != nil {
		t.Fatal(err)
	}
	if err := porterenroll.RemoveRecord(w.p.Home, "demo"); err != nil {
		t.Fatal(err)
	}
	r, err = captureRuntime(context.Background(), w.p.Home, &named, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Installation != nil || !slices.Contains(r.Unresolved, "installation_default_unresolved") {
		t.Fatal("named snapshot invented a default installation")
	}
}

func TestBackupRecordsPrivateNPMTreesAndKeepsDataWhenSourceChanges(t *testing.T) {
	w := newWorld(t)
	rec, _ := porterenroll.ReadRecord(w.p.Home, "demo")
	p, _ := porterenroll.ReadProfile(w.p.Home, "demo")
	prefix := filepath.Join(w.state, "tools/runtime")
	p.Provenance = &profile.Provenance{Kind: profile.ProvenanceNPMGlobal, NPM: &profile.NPMProvenance{Package: "fixture"}}
	p.Body = &profile.Body{Paths: []profile.PathRef{{Root: "state", Glob: "tools/**"}}}
	rec.Provenance = enroll.Provenance{Kind: profile.ProvenanceNPMGlobal, NPM: &enroll.NPMRecord{Package: "fixture", Version: "1.2.3", Prefix: prefix,
		Registry: "https://registry.example.test/", Integrity: "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))}}
	write(t, filepath.Join(prefix, "bin/node"), "private runtime")
	metadata := filepath.Join(prefix, "lib/node_modules/fixture/package.json")
	write(t, metadata, `{"name":"fixture","version":"1.2.3"}`)
	if _, err := porterenroll.WriteRecord(w.p.Home, *rec, *p); err != nil {
		t.Fatal(err)
	}
	first := w.seal(t, vault.TierSoul)
	v, err := vault.Open(w.leaf(t).Dir)
	if err != nil {
		t.Fatal(err)
	}
	header, err := v.SnapshotHeader(first.SnapshotID, w.id)
	if err != nil || header.Runtime == nil || len(header.Runtime.Dependencies) != 1 || slices.Contains(header.Runtime.Unresolved, "program_source_unverified") {
		t.Fatal("private source not recorded", err)
	}
	if !slices.Contains(header.Runtime.Unresolved, "runtime_dependencies_unverified") {
		t.Fatal("private bytes were promoted to proof of every OS dependency")
	}
	files := w.restore(t, first.SnapshotID)
	if string(files["state:SOUL.md"]) != "soul" {
		t.Fatal("runtime scan affected data backup")
	}
	for name := range files {
		if strings.Contains(name, "tools/") {
			t.Fatal("program evidence silently expanded backup tier")
		}
	}
	write(t, metadata, `{"name":"fixture","version":"2.0.0"}`)
	second := w.seal(t, vault.TierSoul)
	v, err = vault.Open(w.leaf(t).Dir)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := v.SnapshotHeader(second.SnapshotID, w.id)
	if err != nil || changed.Runtime == nil || len(changed.Runtime.Dependencies) != 0 || !slices.Contains(changed.Runtime.Unresolved, "program_tree_unavailable") {
		t.Fatal("changed source was guessed or partial evidence recorded", err)
	}
	if string(w.restore(t, second.SnapshotID)["state:SOUL.md"]) != "soul" {
		t.Fatal("source uncertainty prevented data backup")
	}
}
