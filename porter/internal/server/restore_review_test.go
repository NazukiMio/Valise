package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/valise"
	porterrevive "valise/porter/internal/revive"
	"valise/porter/internal/seal"
)

func TestRestoreReviewRequiresIdleServerAndRestrictsMigrationBranches(t *testing.T) {
	home := t.TempDir()
	op := revive.NewOpID(time.Now())
	store := porterrevive.Store{Home: home}
	j := &revive.Journal{Plan: revive.Plan{OpID: op, Mode: revive.ModeMigrate, InstanceID: "demo"}, State: revive.StatePlanned, StageVerified: true, ManifestSaved: true}
	if err := store.Save(j); err != nil {
		t.Fatal(err)
	}
	s := &session{cfg: Config{Home: home, Tier: rpc.TierDaily}, owner: true}
	r, err := restoreReview(s, op)
	if err != nil || len(r.Actions) != 1 || r.Actions[0] != "back" {
		t.Fatalf("migration branches %+v %v", r, err)
	}
	unlock, err := (seal.Paths{Home: home}).LockFor("swap")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	params, _ := json.Marshal(revive.OpParams{OpID: op})
	if _, err = s.guardedCall(context.Background(), rpc.Request{Method: revive.MethodReview, Params: params}, methods[revive.MethodReview]); err == nil {
		t.Fatal("active swap reported as resumable")
	}
}

func TestRecordedProgramInstallStillNeedsManagementLogin(t *testing.T) {
	home := t.TempDir()
	op := revive.NewOpID(time.Now())
	store := porterrevive.Store{Home: home}
	j := &revive.Journal{Plan: revive.Plan{OpID: op, Body: revive.BodyPlan{Action: revive.BodyNPMInstall}}, State: revive.StatePlanned}
	if err := store.Save(j); err != nil {
		t.Fatal(err)
	}
	s := &session{cfg: Config{Home: home, Tier: rpc.TierDaily}, owner: true}
	err := restoreAccess(s, op, revive.MethodSwap, "")
	if e, ok := valise.As(err); !ok || e.Code != rpc.AdminRequiredCode {
		t.Fatalf("daily owner installed programs: %v", err)
	}
	if err := restoreAccess(s, op, revive.MethodResume, "back"); err != nil {
		t.Fatalf("refusing untouched stage should not need management login: %v", err)
	}
	s.cfg.Tier = rpc.TierAdmin
	if err := restoreAccess(s, op, revive.MethodSwap, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreReviewRollbackOffersOnlyBackUntilArchived(t *testing.T) {
	home := t.TempDir()
	store := porterrevive.Store{Home: home}
	s := &session{cfg: Config{Home: home, Tier: rpc.TierDaily}, owner: true}
	for _, state := range []string{revive.StateRollingBack, revive.StateRolledBack} {
		op := revive.NewOpID(time.Now())
		j := &revive.Journal{Plan: revive.Plan{OpID: op, Mode: revive.ModeRollback}, State: state}
		if err := store.Save(j); err != nil {
			t.Fatal(err)
		}
		r, err := restoreReview(s, op)
		if err != nil || r.Complete || len(r.Actions) != 1 || r.Actions[0] != "back" {
			t.Fatalf("rollback branch %+v %v", r, err)
		}
	}
}
