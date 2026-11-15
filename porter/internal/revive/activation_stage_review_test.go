package revive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/valise"
)

func TestSwitchStageReviewIsReadOnlyAndBindsActualFileIdentity(t *testing.T) {
	w := newManagedWorld(t, true)
	w.begin(t)
	j, err := w.r.Store.Load(w.p.OpID)
	if err != nil {
		t.Fatal(err)
	}
	path := w.r.Store.path(w.p.OpID)
	before, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	digest, files, bytes, err := reviewManagedStage(context.Background(), j)
	if err != nil || len(digest) != 64 || files != 1 || bytes != 8 {
		t.Fatal(digest, files, bytes, err)
	}
	again, _, _, err := reviewManagedStage(context.Background(), j)
	after, _ := os.ReadFile(path)
	last, _ := os.Stat(path)
	if err != nil || digest != again || string(before) != string(after) || info.Mode() != last.Mode() || !info.ModTime().Equal(last.ModTime()) || read(t, w.state+"/SOUL.md") != "current soul" {
		t.Fatal("read-only review changed journal/data or produced unstable identity", err)
	}
	stageFile, _ := stageDest(j, "state/SOUL.md")
	if err := os.Chmod(stageFile, 0644); err != nil {
		t.Fatal(err)
	}
	changed, _, _, err := reviewManagedStage(context.Background(), j)
	if err != nil || changed == digest {
		t.Fatal("same bytes with changed permissions retained old review", err)
	}
}

func TestSwitchStageReviewRejectsChangedMissingExtraOrAliasedData(t *testing.T) {
	for _, change := range []string{"content", "missing", "extra", "symlink", "hardlink", "sensitive-mode", "unverified"} {
		t.Run(change, func(t *testing.T) {
			w := newManagedWorld(t, false)
			if change == "sensitive-mode" {
				w.manifest[0].Category = "secrets"
			}
			w.begin(t)
			j, _ := w.r.Store.Load(w.p.OpID)
			path, _ := stageDest(j, "state/SOUL.md")
			switch change {
			case "content":
				write(t, path, "modified", 0600)
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "extra":
				write(t, filepath.Join(filepath.Dir(path), "extra"), "not in manifest", 0600)
			case "symlink":
				if err := os.Rename(path, path+".target"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".target", path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(t.TempDir(), "alias")); err != nil {
					t.Fatal(err)
				}
			case "sensitive-mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "unverified":
				j.StageVerified = false
			}
			_, _, _, err := reviewManagedStage(context.Background(), j)
			if e, ok := valise.As(err); !ok || e.Code != "activation_stage_changed" {
				t.Fatal("changed stage gained a review", change, err)
			}
			if change == "sensitive-mode" {
				st, _ := os.Stat(path)
				if st.Mode().Perm() != 0644 {
					t.Fatal("query repaired file permissions")
				}
			}
		})
	}
}
