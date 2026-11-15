package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/valise"
	porterrevive "valise/porter/internal/revive"
)

func TestEnvironmentRunRequiresSeparateManagementLogin(t *testing.T) {
	if rpc.TierOf(environment.MethodTest) != rpc.TierAdmin {
		t.Fatal("isolated program preparation exposed to daily owner")
	}
	s := &session{cfg: Config{Home: t.TempDir(), Tier: rpc.TierDaily}, owner: true}
	params, _ := json.Marshal(revive.OpParams{OpID: revive.NewOpID(time.Now())})
	_, err := methods[environment.MethodTest](s, context.Background(), params)
	if e, ok := valise.As(err); !ok || e.Code != rpc.AdminRequiredCode {
		t.Fatal("daily owner reached program preparation", err)
	}
}

func TestTestOnlyReviewCannotOfferContinue(t *testing.T) {
	home := t.TempDir()
	op := revive.NewOpID(time.Now())
	j := &revive.Journal{Plan: revive.Plan{OpID: op, Mode: revive.ModeRollback, TestOnly: true}, State: revive.StatePlanned, StageVerified: true, ManifestSaved: true}
	if err := (porterrevive.Store{Home: home}).Save(j); err != nil {
		t.Fatal(err)
	}
	s := &session{cfg: Config{Home: home, Tier: rpc.TierDaily}, owner: true}
	r, err := restoreReview(s, op)
	if err != nil || len(r.Actions) != 1 || r.Actions[0] != "back" {
		t.Fatal("test was offered activation", r, err)
	}
}
