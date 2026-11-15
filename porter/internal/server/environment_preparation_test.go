package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/porter/internal/managedruntime"
	porterrevive "valise/porter/internal/revive"
)

func TestPreparationTransportRequiresManagementAndRejectsExecutionFacts(t *testing.T) {
	for _, method := range []string{environment.MethodPreparationBegin, environment.MethodPreparationFinish, environment.MethodPreparationCancel, environment.MethodPreparationCleanup} {
		t.Run(method, func(t *testing.T) {
			if !rpc.Classified(method) || rpc.TierOf(method) != rpc.TierAdmin || installationMutations[method] {
				t.Fatal("wrong permission or double lock", method)
			}
			s := &session{cfg: Config{Home: t.TempDir(), Tier: rpc.TierDaily}, owner: true}
			_, err := methods[method](s, context.Background(), json.RawMessage(`{"verified":true,"argv":["/bin/sh"]}`))
			if e, ok := valise.As(err); !ok || e.Code != rpc.AdminRequiredCode {
				t.Fatal("daily owner reached preparation", err)
			}
		})
	}
	p := environment.PreparationParams{Agent: identity.AgentRef{ServerUID: "server", InstallID: "demo", InstanceID: "demo", Generation: "one"}, OperationID: strings.Repeat("a", 32)}
	for _, method := range []string{environment.MethodPreparationFinish, environment.MethodPreparationStatus, environment.MethodPreparationCancel, environment.MethodPreparationCleanupReview} {
		raw, _ := json.Marshal(p)
		raw = append(raw[:len(raw)-1], []byte(`,"argv":["/bin/sh"],"gateway_passed":true}`)...)
		s := &session{cfg: Config{Home: t.TempDir(), Tier: rpc.TierAdmin}}
		if _, err := methods[method](s, context.Background(), raw); err != errInvalidParams {
			t.Fatal("supplied execution facts reached controller", method, err)
		}
	}
}

func TestPreparationStatusIsReadOnlyAndProjectsNoPrivateRecords(t *testing.T) {
	home := t.TempDir()
	s := &session{cfg: Config{Home: home, Tier: rpc.TierDaily}}
	p := environment.PreparationParams{Agent: identity.AgentRef{ServerUID: "server", InstallID: "demo", InstanceID: "demo", Generation: "one"}, OperationID: strings.Repeat("a", 32)}
	raw, _ := json.Marshal(p)
	for _, method := range []string{environment.MethodPreparationStatus, environment.MethodPreparationCleanupReview} {
		if !rpc.Classified(method) || rpc.TierOf(method) != rpc.TierDaily {
			t.Fatal("read-only query gained management authority")
		}
		if _, err := methods[method](s, context.Background(), raw); err == nil {
			t.Fatal("missing record was invented")
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		t.Fatal("query created private state", err)
	}
	v := &porterrevive.PreparationReview{Agent: p.Agent, OperationID: p.OperationID, State: "prepared", Native: &managedruntime.Observation{Running: true, Unit: "PRIVATE_UNIT", Definition: "PRIVATE_DIGEST", KernelStart: "PRIVATE_PROCESS"}, CanCancel: true}
	projected := projectPreparation(v)
	encoded, _ := json.Marshal(projected)
	if !projected.OriginalObserved || !projected.OriginalRunning || projected.OriginalStopped || strings.Contains(string(encoded), "PRIVATE_") {
		t.Fatal("projection lost facts or returned internal evidence", string(encoded))
	}
	v.Native = nil
	v.OriginalManaged = &managedruntime.Observation{Running: true, MainPID: 999, Definition: "PRIVATE_MANAGED"}
	projected = projectPreparation(v)
	encoded, _ = json.Marshal(projected)
	if !projected.OriginalObserved || !projected.OriginalRunning || strings.Contains(string(encoded), "PRIVATE_") {
		t.Fatal("managed original projection lost or leaked facts", string(encoded))
	}
	v.OriginalManaged = nil
	projected = projectPreparation(v)
	if projected.OriginalObserved {
		t.Fatal("candidate observation substituted for original")
	}
}
