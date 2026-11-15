//go:build linux

package revive

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/vault"
	"valise/porter/internal/activeview"
	"valise/porter/internal/managedruntime"
)

func privateReadFingerprint(t *testing.T, home string) string {
	t.Helper()
	type file struct {
		Path     string
		Mode     uint32
		Size     int64
		Modified int64
		Hash     string
	}
	var files []file
	err := filepath.WalkDir(filepath.Join(home, ".local/state/valise"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		f := file{Path: path, Mode: uint32(st.Mode()), Size: st.Size(), Modified: st.ModTime().UnixNano()}
		if st.Mode().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f.Hash = vault.SHA256Hex(b)
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	return vault.SHA256Hex(b)
}

func assertActiveRead(t *testing.T, ctx context.Context, home string, control managedruntime.Controller, agent identity.AgentRef, ref environment.Ref, native bool) {
	t.Helper()
	before := privateReadFingerprint(t, home)
	reader := activeview.Reader{Home: home, Control: control}
	for range 2 {
		current, err := reader.Read(ctx, agent)
		if err != nil || current == nil || current.Ref != ref || (current.Native != nil) != native || !current.Current.Running || !current.Gateway.GatewayPassed || current.Operation.Confirmed {
			t.Fatal("daily binding does not match actual runtime", current, err)
		}
		summary, units, entries := reader.Summary(ctx, agent)
		if summary == nil || !summary.Known || !summary.Running || !summary.GatewayPassed || summary.Native != native || summary.ConversationConfirmed || len(units) != 1 || len(entries) != 1 || !entries[0].Listening {
			t.Fatal("daily summary confused runtime or conversation evidence", summary, units, entries)
		}
	}
	if privateReadFingerprint(t, home) != before {
		t.Fatal("daily read rewrote private state or programme workspace")
	}
}

func assertActiveDataRead(t *testing.T, ctx context.Context, home string, agent identity.AgentRef, ref environment.Ref, native bool) {
	t.Helper()
	before := privateReadFingerprint(t, home)
	reader := activeview.Reader{Home: home}
	for range 2 {
		data, err := reader.ReadData(ctx, agent)
		if err != nil || data == nil || data.Ref != ref || data.Native != native || data.Program == nil || data.Record.Roots["state"] == "" {
			t.Fatal("daily data read lost stable source", data, err)
		}
		if err := reader.ValidateData(ctx, data); err != nil {
			t.Fatal("daily data binding changed", err)
		}
	}
	if privateReadFingerprint(t, home) != before {
		t.Fatal("daily data query rewrote private state")
	}
}
