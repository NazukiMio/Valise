package environments

// This separate opt-in reads only the registered private program tree. It does
// not use Agent data, install a harness, stop a service or write a vault. An
// explicit runtime-profile additionally starts only the copied private program.
import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/vault"
	"valise/porter/internal/isolated"

	porterenroll "valise/porter/internal/enroll"
)

var installedSourceLab = flag.Bool("installed-source-lab", false, "observe/freeze registered private npm source on a real Linux lab")
var installedInstance = flag.String("installed-instance", "", "explicit default instance for read-only program material lab")
var installedRuntimeProfile = flag.String("runtime-profile", "", "explicit profile for private program smoke test using only empty synthetic data")

func TestRegisteredInstalledSourceLab(t *testing.T) {
	if !*installedSourceLab {
		t.Skip("requires explicit real source lab run")
	}
	home, err := os.UserHomeDir()
	if err != nil || *installedInstance == "" {
		t.Fatal("explicit source instance and HOME required")
	}
	d, p, err := porterenroll.ReadInstallation(home, *installedInstance)
	if err != nil || d == nil || p == nil {
		t.Fatal("registered source unavailable", err)
	}
	r := d.Record(*installedInstance)
	if r == nil || r.Name != "" {
		t.Fatal("source lab requires an explicit default installation")
	}
	if *installedRuntimeProfile != "" {
		doc, err := profile.LoadFile(*installedRuntimeProfile)
		if err != nil || doc.Profile.ID != p.ID || doc.Profile.Environments == nil {
			t.Fatal("explicit private runtime profile unavailable", err)
		}
		if Digest(doc.Profile.Body) != Digest(p.Body) || Digest(doc.Profile.Provenance) != Digest(p.Provenance) || Digest(doc.Profile.Service) != Digest(p.Service) || Digest(doc.Profile.Paths) != Digest(p.Paths) || Digest(doc.Profile.Roots) != Digest(p.Roots) {
			t.Fatal("runtime fixture changed registered source/data scope")
		}
		p = doc.Profile
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	in := InstalledSource{Profile: *p, Layout: Layout{Home: home, Roots: r.Roots}, Origin: d.Provenance, Overrides: r.LocalOverrides, MaxBytes: 8 << 30}
	observed, err := ObserveInstalledNPM(ctx, in)
	if err != nil {
		t.Fatal("registered private source did not validate", err)
	}
	header := observedHeader(t, in, observed)
	archive, err := FreezeInstalledNPM(ctx, t.TempDir(), in, header)
	if err != nil {
		t.Fatal("registered source could not be frozen independently", err)
	}
	defer archive.File.Close()
	if archive.Material.SourceDigest != observed.Material.SourceDigest || archive.Material.ArchiveBytes == 0 {
		t.Fatal("source/material receipt missing")
	}
	var bytes int64
	for _, f := range archive.Material.Files {
		bytes += f.Size
	}
	t.Logf("private_files=%d program_bytes=%d archive_bytes=%d", len(archive.Material.Files), bytes, archive.Material.ArchiveBytes)
	if *installedRuntimeProfile == "" {
		return
	}
	current := *p
	in.Layout.RuntimeExecutable = archive.RuntimeExecutable
	now := time.Now().UTC()
	// This is explicitly NOT an actual backup/identity: only real program
	// materials and empty synthetic data. No live Agent file is opened.
	target := environment.Material{Source: environment.Source{Agent: identity.AgentRef{ServerUID: "runtime-lab", InstallID: "runtime-lab", InstanceID: "runtime-lab", Generation: "runtime-lab"},
		Snapshot: identity.SnapshotRef{VaultID: vault.NewID("v", now), SnapshotID: vault.NewID("s", now)}, Time: now, Tier: vault.TierFull,
		ProfileDigest: Digest(current), LayoutDigest: LayoutDigest(in.Layout, in.Overrides), ProgramDigest: archive.Material.SourceDigest, Verified: true}, Files: vault.Files{Format: vault.FormatFiles}}
	candidate, err := Build(ctx, t.TempDir(), BuildInput{Report: func(stage string) { t.Log("build_stage=" + stage) }, Profile: current, Overrides: in.Overrides, Layout: in.Layout, Target: target, Program: archive.Material, Archive: archive.File, MaxBytes: in.MaxBytes})
	if err != nil {
		t.Fatal("private runtime material build failed", err)
	}
	check, err := isolated.CheckFor(current)
	if err != nil {
		t.Fatal("private gateway rule unavailable", err)
	}
	result, err := isolated.GatewayProbe(ctx, candidate.Workspace, candidate.Spec, check)
	if err != nil || !result.Verified() {
		t.Fatal("private real-program smoke test did not reach declared gateway readiness", result, err)
	}
	t.Logf("private_runtime_started=%t http_status=%d stopped=%t", result.ProcessStarted, result.HTTPStatus, result.ProcessesStopped)
	if observed.runtime != nil {
		pid, err := runtimeMainPID(ctx, in.Layout.Home, current.Environments.RuntimeExecutable.Service.Unit)
		if err != nil || pid != observed.runtime.PID {
			t.Fatal("original service changed during private smoke test")
		}
		start, err := runtimeProcessStart(pid)
		if err != nil || start != observed.runtime.Start {
			t.Fatal("original process no longer matched its start identity")
		}
		t.Log("original_service_still_running=true")
	}
}
