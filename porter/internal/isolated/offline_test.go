package isolated

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"valise/core/profile"
)

func offlineFixture() profile.Profile {
	return profile.Profile{Environments: &profile.Environments{OfflineReadiness: &profile.OfflineReadiness{
		Kind: "unix-json", Socket: "${STATE_DIR}/gateway.sock", Request: `{"protocol":1,"verb":"identify","id":1}`,
		Checks: []profile.ReadinessField{
			{Field: "ok", Kind: "boolean_equal", Value: "true"},
			{Field: "protocol", Kind: "integer_equal", Value: "1"},
			{Field: "id", Kind: "integer_equal", Value: "1"},
			{Field: "result.runtime_protocol", Kind: "integer_equal", Value: "1"},
			{Field: "result.state", Kind: "string_equal", Value: "ready"},
			{Field: "result.capabilities", Kind: "string_array_contains", Value: "session-authority-v1"},
			{Field: "result.authority_epoch", Kind: "positive_integer"},
			{Field: "result.instance_id", Kind: "nonempty_string"},
			{Field: "result.api_origin", Kind: "loopback_http_origin"},
			{Field: "result.code_sha", Kind: "string_equal", Value: "${PROGRAM_REVISION}"},
		}, Members: []profile.ReadinessMember{{Field: "result.served_profiles", MatchField: "home", MatchValue: "${STATE_DIR}",
			Checks: []profile.ReadinessField{{Field: "profile_id", Kind: "nonempty_string"}}}},
	}}}
}

const readyResponse = `{"ok":true,"protocol":1,"id":1,"result":{"runtime_protocol":1,"state":"ready","capabilities":["session-authority-v1"],"authority_epoch":3,"instance_id":"instance","api_origin":"http://127.0.0.1:12345","code_sha":"recorded-revision","served_profiles":[{"home":"/source/home/.fixture","profile_id":"target"}]}}`

func TestOfflineBindsExactSourceAndTarget(t *testing.T) {
	p := offlineFixture()
	check, err := CheckForRuntime(p, "/source/home", "/source/home/.fixture", "recorded-revision")
	if err != nil || !check.validAt("/source/home") {
		t.Fatal("source binding failed", err)
	}
	object, ok := uniqueJSON([]byte(readyResponse))
	if !ok || !offlineMatches(object, check.Offline) {
		t.Fatal("public offline response did not match")
	}
	if p.Environments.OfflineReadiness.Checks[9].Value != "${PROGRAM_REVISION}" {
		t.Fatal("binding mutated signed declaration")
	}
	if _, err := CheckForRuntime(p, "/source/home", "/source/home/.fixture", ""); err != ErrWorkspace {
		t.Fatal("missing historical revision was guessed", err)
	}
	if _, err := CheckForRuntime(p, "/source/home", "/another/agent", "recorded-revision"); err != ErrWorkspace {
		t.Fatal("external state root accepted", err)
	}
	if _, err := CheckForRuntime(p, "/source/home", "/source/home/"+strings.Repeat("x", 110), "recorded-revision"); err != ErrWorkspace {
		t.Fatal("unusable Unix path accepted", err)
	}
	if check.validAt("/another/home") {
		t.Fatal("cross-workspace socket accepted")
	}
	check.Statuses = []int{200}
	if check.valid() {
		t.Fatal("HTTP and offline proof mixed")
	}
}

func TestOfflineRejectsAmbiguousWrongOrIncompleteMetadata(t *testing.T) {
	check, err := CheckForRuntime(offlineFixture(), "/source/home", "/source/home/.fixture", "recorded-revision")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ old, replacement string }{
		{`"ok":true`, `"ok":"true"`},
		{`"protocol":1`, `"protocol":true`},
		{`"id":1`, `"id":2`},
		{`"runtime_protocol":1`, `"runtime_protocol":1.0`},
		{`"state":"ready"`, `"state":"draining"`},
		{`"session-authority-v1"`, `"unrelated"`},
		{`["session-authority-v1"]`, `["session-authority-v1",7]`},
		{`"authority_epoch":3`, `"authority_epoch":0`},
		{`"authority_epoch":3`, `"authority_epoch":999999999999999999999999999999999999`},
		{`"instance_id":"instance"`, `"instance_id":""`},
		{`"http://127.0.0.1:12345"`, `"http://10.0.0.1:12345"`},
		{`"http://127.0.0.1:12345"`, `"http://token@127.0.0.1:12345"`},
		{`"http://127.0.0.1:12345"`, `"http://127.0.0.1:12345/"`},
		{`"code_sha":"recorded-revision"`, `"code_sha":"current-but-wrong-revision"`},
		{`"home":"/source/home/.fixture"`, `"home":"/source/home/.other"`},
		{`"profile_id":"target"`, `"profile_id":""`},
		{`[{"home":"/source/home/.fixture","profile_id":"target"}]`, `[{"home":"/source/home/.fixture","profile_id":"target"},{"home":"/source/home/.fixture","profile_id":"other"}]`},
		{`"ok":true`, `"ok":false,"ok":true`},
	} {
		t.Run(tc.replacement, func(t *testing.T) {
			object, ok := uniqueJSON([]byte(strings.Replace(readyResponse, tc.old, tc.replacement, 1)))
			if ok && offlineMatches(object, check.Offline) {
				t.Fatal("unreliable offline proof accepted")
			}
		})
	}
	for _, data := range []string{`{}`, `{"result":{"state":"ready"}}`, readyResponse + `{}`, readyResponse[:len(readyResponse)-1], strings.Repeat(`[`, 40) + `0` + strings.Repeat(`]`, 40)} {
		if object, ok := uniqueJSON([]byte(data)); ok && offlineMatches(object, check.Offline) {
			t.Fatal("incomplete response became gateway proof")
		}
	}
}

func TestOfflineProofNeverClaimsHTTPOrHumanAcceptance(t *testing.T) {
	r := ProbeResult{ProofKind: "unix-json", ProcessStarted: true, SocketListening: true, OfflineResponded: true, GatewayPassed: true, ProcessesStopped: true, Completed: true}
	if !r.Verified() || r.HTTPResponded || r.PortListening {
		t.Fatal("offline readiness lost its separate evidence", r)
	}
	r.ProcessesStopped = false
	if r.Verified() {
		t.Fatal("unknown cleanup passed")
	}
	check, _ := CheckForRuntime(offlineFixture(), "/source/home", "/source/home/.fixture", "recorded-revision")
	check.Timeout = time.Hour
	if check.valid() {
		t.Fatal("unbounded offline test allowed")
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "recorded-revision") {
		t.Fatal("response metadata escaped the internal check")
	}
}
