package server

import (
	"context"
	"encoding/json"
	"testing"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/porter/internal/seal"
)

func TestRestoreSettingsOwnerAndMutationLock(t *testing.T) {
	s := &session{cfg: Config{Home: t.TempDir(), Tier: rpc.TierDaily}}
	p, _ := json.Marshal(environment.SetSettingsParams{Agent: identity.AgentRef{InstanceID: "agent"}, Mode: environment.Direct, Expected: "stale"})
	_, err := methods[environment.MethodSettingsSet](s, context.Background(), p)
	if e, ok := valise.As(err); !ok || e.Code != rpc.OwnerRequiredCode {
		t.Fatal("daily channel changed policy", err)
	}
	s.owner = true
	unlock, err := (seal.Paths{Home: s.cfg.Home}).LockFor("test")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, err = s.guardedCall(context.Background(), rpc.Request{Method: environment.MethodSettingsSet, Params: p}, methods[environment.MethodSettingsSet])
	if e, ok := valise.As(err); !ok || e.Code != "vault_busy" {
		t.Fatal("settings raced restore", err)
	}
}
