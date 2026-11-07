package actionjournal

import (
	"errors"
	"os"
	"path/filepath"

	"valise/core/action"
)

// Reconcile recognizes every rename window from recorded device/inode identities.
// It never runs a configuration step or guesses from path existence alone.
func (s Store) Reconcile(j *action.Operation) error {
	if err := ValidatePaths(j); err != nil {
		return err
	}
	live, le := DirectoryIdentity(j.Plan.Live)
	prev, pe := DirectoryIdentity(j.Previous)
	stage, se := DirectoryIdentity(j.Stage)
	sameLive := le == nil && live == j.Plan.LiveIdentity
	samePrev := pe == nil && prev == j.Plan.LiveIdentity
	sameStage := se == nil && stage == j.StageIdentity && stage.Inode != 0
	published := le == nil && live == j.StageIdentity && live.Inode != 0
	absent := func(err error) bool { return errors.Is(err, os.ErrNotExist) }
	switch {
	case sameLive && absent(pe) && (sameStage || absent(se)):
		if j.Published {
			return invalid()
		}
		return nil
	case absent(le) && samePrev && sameStage:
		if j.Phase != "move-live-intent" && j.Phase != "live-moved" && j.Phase != "publish-intent" {
			return invalid()
		}
		// Restore the old name; user can then choose continue or abandon.
		if err := os.Rename(j.Previous, j.Plan.Live); err != nil {
			return err
		}
		if err := syncDir(filepath.Dir(j.Plan.Live)); err != nil {
			return err
		}
		j.Phase = "ready"
		return s.Save(j)
	case published && samePrev && absent(se):
		if j.Phase != "publish-intent" && !j.Published {
			return invalid()
		}
		j.Published = true
		j.Phase = "published"
		return s.Save(j)
	default:
		return invalid()
	}
}

// ValidatePaths restricts staging and previous directories to operation-owned
// sibling locations. This is checked again before any rename or cleanup.
func ValidatePaths(j *action.Operation) error {
	if !action.ValidOperationID(j.ID) || j.Plan.LiveIdentity.Inode == 0 || !filepath.IsAbs(j.Plan.Live) || filepath.Clean(j.Plan.Live) != j.Plan.Live || j.Plan.Live == string(filepath.Separator) {
		return invalid()
	}
	parent := filepath.Dir(j.Plan.Live)
	private := filepath.Join(parent, ".valise-action-"+j.ID)
	if j.Stage != filepath.Join(private, "stage") || j.Previous != filepath.Join(private, "prev") {
		return invalid()
	}
	st, err := os.Lstat(private)
	if errors.Is(err, os.ErrNotExist) && j.Done && j.CleanupStarted {
		return nil // An explicitly requested cleanup may have removed the wrapper before saving completion.
	}
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		return invalid()
	}
	return nil
}
func (s Store) Publish(j *action.Operation) error {
	if !j.StageVerified || len(j.Steps) != len(j.Plan.Definition.Steps) {
		return invalid()
	}
	for _, step := range j.Steps {
		if !step.Completed || step.Exit == nil || *step.Exit != 0 {
			return invalid()
		}
	}
	if err := s.Reconcile(j); err != nil {
		return err
	}
	if j.Published {
		return nil
	}
	id, err := DirectoryIdentity(j.Stage)
	if err != nil || id != j.StageIdentity {
		return invalid()
	}
	allow := OriginalTexts(j.Plan.Links)
	liveTree, err := TreeDigestAllow(j.Plan.Live, allow)
	if err != nil || j.LiveTreeDigest == "" || liveTree != j.LiveTreeDigest {
		return invalid()
	}
	tree, err := TreeDigestAllow(j.Stage, Merged(allow, j.WriteLinks))
	if err != nil || j.StageTreeDigest == "" || tree != j.StageTreeDigest {
		return invalid()
	}
	if err := SyncTree(j.Stage); err != nil {
		return err
	}
	j.Phase = "move-live-intent"
	if err = s.Save(j); err != nil {
		return err
	}
	if err = os.Rename(j.Plan.Live, j.Previous); err != nil {
		return err
	}
	// Source and destination parents must both be durable.
	if err = syncDir(filepath.Dir(j.Plan.Live)); err != nil {
		return err
	}
	if err = syncDir(filepath.Dir(j.Previous)); err != nil {
		return err
	}
	j.Phase = "publish-intent"
	if err = s.Save(j); err != nil {
		return err
	}
	if err = os.Rename(j.Stage, j.Plan.Live); err != nil {
		return err
	}
	if err = syncDir(filepath.Dir(j.Plan.Live)); err != nil {
		return err
	}
	if err = syncDir(filepath.Dir(j.Stage)); err != nil {
		return err
	}
	j.Published = true
	j.Phase = "published"
	return s.Save(j)
}
