//go:build linux

package revive

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
	"valise/porter/internal/managedruntime"
)

func TestNativeEnvironmentPlanUsesRealSourceWithoutMutationLab(t *testing.T) {
	if !*activationLab {
		t.Skip("requires explicitly authorized Linux source planning lab")
	}
	for _, scenario := range []string{"matching-source", "changed-source", "changed-settings", "changed-layout", "wrong-generation"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			program, err := os.ReadFile(exe)
			if err != nil {
				t.Fatal(err)
			}
			id, err := identity.NewID()
			if err != nil {
				t.Fatal(err)
			}
			unit := profile.ServiceUnit{Scope: "user", Unit: "fixture-native-" + id + ".service"}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			w, p := environmentWorldProfile(t, program, func(p *profile.Profile) {
				p.Service = &profile.Service{Candidates: []profile.ServiceUnit{unit}}
				p.Entries[0].Port = port
			})
			for _, path := range []string{w.home, w.state} {
				if err := os.Chmod(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			control := managedruntime.Controller{Home: home}
			ref, before := startNativeActivationFixture(t, control, w.p, unit, program, w.state, port, false, filepath.Join(w.state, "bin/gateway"))
			binding, err := porteridentity.Read(w.home, "demo")
			if err != nil || binding.Agent == nil {
				t.Fatal(err)
			}
			settings, err := (environments.Store{Home: w.home}).Settings(binding.Agent.Ref)
			if err != nil {
				t.Fatal(err)
			}
			req := revive.EnvironmentPlanParams{Agent: binding.Agent.Ref, SettingsDigest: settings.Digest, Input: *p.IsolatedTest}
			switch scenario {
			case "changed-source":
				write(t, filepath.Join(w.state, "tools/npm/lib/node_modules/fixture/package.json"), `{"name":"fixture","version":"new-version"}`, 0600)
			case "changed-settings":
				if _, err := (environments.Store{Home: w.home}).SetSettings(environment.SetSettingsParams{Agent: settings.Agent, Expected: settings.Digest, Mode: environment.Compact}); err != nil {
					t.Fatal(err)
				}
			case "changed-layout":
				req.Input.Header.Roots["state"] += "-other"
			case "wrong-generation":
				req.Agent.Generation = "different"
			}
			view, err := w.r.planEnvironment(ctx, req, control)
			if scenario == "matching-source" {
				if err != nil || view == nil || !view.NativeRunning || !view.ProgramMatches || view.Snapshot.SnapshotID != p.SnapshotID || view.ProgramVersion != "1.2.3" || view.Files != 1 || view.Bytes != 8 || !view.ProfileUntested || len(view.Space) != 1 || view.Space[0].NeedBytes <= uint64(view.ProgramBytes)*4 {
					t.Fatal("real source planning failed", view, err)
				}
			} else if err == nil || view != nil {
				t.Fatal("changed evidence accepted", scenario, view, err)
			}
			after, err := control.ReadNative(ctx, ref)
			if err != nil || !after.Running || after.MainPID != before.MainPID || after.KernelStart != before.KernelStart || after.UnitFileState != before.UnitFileState {
				t.Fatal("read-only preview disturbed native A", after, err)
			}
			if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
				t.Fatal("preview wrote active data")
			}
			for _, dir := range []string{"environments", "journal", "programs", "activations", "environment-tests", "environment-preparations"} {
				_, err := os.Lstat(filepath.Join(w.home, ".local/state/valise", dir))
				if scenario == "changed-settings" && dir == "environments" {
					continue
				}
				if !os.IsNotExist(err) {
					t.Fatal("preview initialized operation state", dir, err)
				}
			}
		})
	}
}
