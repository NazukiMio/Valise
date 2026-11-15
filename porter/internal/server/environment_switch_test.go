package server

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/rpc"
	"valise/core/valise"
)

func TestSwitchTransportRequiresManagementAndRejectsSuppliedEvidence(t *testing.T) {
	for _, method := range []string{environment.MethodSwitch, environment.MethodReturn, environment.MethodConfirm, environment.MethodOldCleanup} {
		if !rpc.Classified(method) || rpc.TierOf(method) != rpc.TierAdmin || installationMutations[method] {
			t.Fatal("wrong tier or double locking", method)
		}
		s := &session{cfg: Config{Home: t.TempDir(), Tier: rpc.TierDaily}, owner: true}
		if _, err := methods[method](s, context.Background(), json.RawMessage(`{}`)); err == nil {
			t.Fatal("daily owner reached a service switch")
		} else if e, ok := valise.As(err); !ok || e.Code != rpc.AdminRequiredCode {
			t.Fatal(err)
		}
		s.cfg.Tier = rpc.TierAdmin
		raw, _ := json.Marshal(environment.ReviewedOperationParams{PreparationParams: environment.PreparationParams{Agent: identity.AgentRef{ServerUID: "server", InstallID: "demo", InstanceID: "demo", Generation: "one"}, OperationID: strings.Repeat("a", 32)}, Digest: strings.Repeat("b", 64)})
		raw = append(raw[:len(raw)-1], []byte(`,"confirmed":true,"argv":["/bin/sh"],"gateway_passed":true}`)...)
		if _, err := methods[method](s, context.Background(), raw); err != errInvalidParams {
			t.Fatal("supplied evidence reached the executor", method, err)
		}
	}
}

func TestSwitchReadQueriesDoNotCreateMissingRecordsOrAdvertiseIncompleteLifecycle(t *testing.T) {
	home := t.TempDir()
	s := &session{cfg: Config{Home: home, Tier: rpc.TierDaily}}
	raw, _ := json.Marshal(environment.PreparationParams{Agent: identity.AgentRef{ServerUID: "server", InstallID: "demo", InstanceID: "demo", Generation: "one"}, OperationID: strings.Repeat("c", 32)})
	for _, method := range []string{environment.MethodSwitchReview, environment.MethodSwitchStatus, environment.MethodOldCleanupReview} {
		if !rpc.Classified(method) || rpc.TierOf(method) != rpc.TierDaily {
			t.Fatal("read tier changed")
		}
		if _, err := methods[method](s, context.Background(), raw); err == nil {
			t.Fatal("missing operation was invented")
		}
	}
	current, _ := json.Marshal(environment.SettingsParams{Agent: identity.AgentRef{ServerUID: "server", InstallID: "demo", InstanceID: "demo", Generation: "one"}})
	if !rpc.Classified(environment.MethodCurrentOldCleanup) || rpc.TierOf(environment.MethodCurrentOldCleanup) != rpc.TierDaily {
		t.Fatal("current cleanup discovery gained management authority")
	}
	if _, err := methods[environment.MethodCurrentOldCleanup](s, context.Background(), current); err == nil {
		t.Fatal("cleanup discovery invented registry")
	}
	current = append(current[:len(current)-1], []byte(`,"operation_id":"supplied","confirmed":true}`)...)
	if _, err := methods[environment.MethodCurrentOldCleanup](s, context.Background(), current); err != errInvalidParams {
		t.Fatal("cleanup discovery accepted supplied operation or evidence", err)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatal("query initialized runtime metadata", entries, err)
	}
	params, _ := json.Marshal(rpc.HelloParams{Protocol: rpc.ProtocolVersion})
	response := hello(context.Background(), rpc.Request{JSONRPC: rpc.JSONRPCVersion, ID: json.RawMessage(`1`), Method: rpc.MethodHello, Params: params}, Config{Home: home, DetectTools: func(context.Context) []rpc.Tool { return nil }})
	var advertised rpc.HelloResult
	if response.Error != nil || json.Unmarshal(response.Result, &advertised) != nil || slices.Contains(advertised.Capabilities, environment.SwitchCapability) {
		t.Fatal("incomplete acceptance/cleanup/reuse advertised as ready", response.Error, advertised.Capabilities)
	}
}
