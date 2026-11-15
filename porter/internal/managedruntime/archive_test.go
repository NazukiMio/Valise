package managedruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"valise/core/identity"
)

func archiveAgent() identity.AgentRef {
	return identity.AgentRef{ServerUID: "server", InstallID: "fixture", InstanceID: "fixture", Generation: "generation"}
}

func TestRememberedDefinitionIsImmutableAndReadOnly(t *testing.T) {
	d, _, _, _ := definitionFixture(t)
	c := Controller{Home: t.TempDir()}
	if err := os.Chmod(c.Home, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Remembered(archiveAgent(), d.Digest()); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(c.Home)
	if len(entries) != 0 {
		t.Fatal("archive read initialized directories")
	}
	if err := c.Remember(d); err != nil {
		t.Fatal(err)
	}
	if err := c.Remember(d); err != nil {
		t.Fatal("idempotent remember", err)
	}
	got, err := c.Remembered(archiveAgent(), d.Digest())
	if err != nil || got.Content() != d.Content() {
		t.Fatal(got, err)
	}
	other := archiveAgent()
	other.Generation = "other"
	if _, err := c.Remembered(other, d.Digest()); err == nil {
		t.Fatal("definition shared across generations")
	}
	if _, err := os.Stat(filepath.Join(c.Home, ".config/systemd/user")); !os.IsNotExist(err) {
		t.Fatal("archive installed a unit")
	}
	var injected Definition
	if err := json.Unmarshal([]byte(`{"name":"arbitrary","content":"command","digest":"claimed"}`), &injected); err != nil {
		t.Fatal(err)
	}
	if err := c.Remember(injected); err == nil {
		t.Fatal("JSON supplied a compiled definition")
	}
}

func TestDefinitionArchiveRejectsChangedExposedAndAliasedFiles(t *testing.T) {
	for _, change := range []string{"content", "permissions", "symlink", "hardlink", "parent_symlink"} {
		t.Run(change, func(t *testing.T) {
			d, _, _, _ := definitionFixture(t)
			c := Controller{Home: t.TempDir()}
			if err := os.Chmod(c.Home, 0700); err != nil {
				t.Fatal(err)
			}
			if err := c.Remember(d); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(c.definitionDir(d.Name()), d.Digest()+".service")
			var err error
			switch change {
			case "content":
				err = os.WriteFile(path, []byte("changed"), 0600)
			case "permissions":
				err = os.Chmod(path, 0644)
			case "symlink":
				err = os.Rename(path, path+".original")
				if err == nil {
					err = os.Symlink(path+".original", path)
				}
			case "hardlink":
				err = os.Link(path, filepath.Join(t.TempDir(), "link"))
			case "parent_symlink":
				dir := c.definitionDir(d.Name())
				err = os.Rename(dir, dir+".original")
				if err == nil {
					err = os.Symlink(dir+".original", dir)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Remembered(archiveAgent(), d.Digest()); err == nil {
				t.Fatal("bad archive accepted", change)
			}
			if err := c.Remember(d); err == nil {
				t.Fatal("bad archive silently overwritten", change)
			}
		})
	}
}
