package environments

// The identify fixture follows the pinned official protocol documented in
// REFERENCES.md. It is synthetic; no actual Agent or chat is touched.
import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/identity"
	"valise/core/profile"
	"valise/core/vault"
	"valise/porter/internal/isolated"
)

func offlineLabDeclaration() *profile.OfflineReadiness {
	return &profile.OfflineReadiness{Kind: "unix-json", Socket: "${STATE_DIR}/gateway.sock", Request: `{"protocol":1,"verb":"identify","id":1}`,
		Checks: []profile.ReadinessField{
			{Field: "ok", Kind: "boolean_equal", Value: "true"}, {Field: "protocol", Kind: "integer_equal", Value: "1"}, {Field: "id", Kind: "integer_equal", Value: "1"},
			{Field: "result.runtime_protocol", Kind: "integer_equal", Value: "1"}, {Field: "result.state", Kind: "string_equal", Value: "ready"},
			{Field: "result.capabilities", Kind: "string_array_contains", Value: "session-authority-v1"}, {Field: "result.authority_epoch", Kind: "positive_integer"},
			{Field: "result.instance_id", Kind: "nonempty_string"}, {Field: "result.api_origin", Kind: "loopback_http_origin"},
			{Field: "result.code_sha", Kind: "string_equal", Value: "${PROGRAM_REVISION}"},
		}, Members: []profile.ReadinessMember{{Field: "result.served_profiles", MatchField: "home", MatchValue: "${STATE_DIR}",
			Checks: []profile.ReadinessField{{Field: "profile_id", Kind: "nonempty_string"}}}},
	}
}

func serveOfflineFixture(t *testing.T, mode string) {
	t.Helper()
	// This listener belongs to the synthetic harness, never to porter.
	listener, err := net.Listen("unix", "gateway.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	permissions := os.FileMode(0o600)
	if mode == "offline_public_socket" {
		permissions = 0o666
	}
	if err := os.Chmod("gateway.sock", permissions); err != nil {
		t.Fatal(err)
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		line, err := bufio.NewReader(io.LimitReader(conn, 4097)).ReadString('\n')
		if err != nil || line != `{"protocol":1,"verb":"identify","id":1}`+"\n" {
			conn.Close()
			t.Fatal("probe did not use the fixed public metadata request")
		}
		payload := map[string]any{"ok": true, "protocol": 1, "id": 1, "result": map[string]any{
			"runtime_protocol": 1, "state": "ready", "capabilities": []string{"session-authority-v1"}, "authority_epoch": 3,
			"instance_id": "fixture-instance", "api_origin": "http://127.0.0.1:12345", "code_sha": "recorded-revision",
			"served_profiles":         []map[string]string{{"home": "/source/home/.fixture", "profile_id": "target"}},
			"ignored_unrelated_field": "synthetic-credential-never-capture",
		}}
		result := payload["result"].(map[string]any)
		switch mode {
		case "offline_wrong_target":
			result["served_profiles"] = []map[string]string{{"home": "/source/home/.other", "profile_id": "other"}}
		case "offline_ambiguous_target":
			result["served_profiles"] = []map[string]string{{"home": "/source/home/.fixture", "profile_id": "one"}, {"home": "/source/home/.fixture", "profile_id": "two"}}
		case "offline_wrong_version":
			result["code_sha"] = "unrelated-current-version"
		case "offline_not_ready":
			result["state"] = "draining"
		case "offline_pid_only":
			payload["result"] = map[string]any{"pid": 100, "running": true}
		}
		data, _ := json.Marshal(payload)
		switch mode {
		case "offline_duplicate_json":
			data = []byte(strings.Replace(string(data), `"ok":true`, `"ok":false,"ok":true`, 1))
		case "offline_oversized":
			data = []byte(`{"extra":"` + strings.Repeat("x", 512<<10) + `"}`)
		case "offline_no_newline":
			conn.Write(data)
			conn.Close()
			continue
		}
		conn.Write(append(data, '\n'))
		conn.Close()
	}
}

func TestPrivateCandidateOfflineLab(t *testing.T) {
	if !*candidateLab {
		t.Skip("requires explicit real Linux lab run")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"offline_success", "offline_wrong_target", "offline_ambiguous_target", "offline_wrong_version", "offline_not_ready", "offline_pid_only", "offline_duplicate_json", "offline_oversized", "offline_no_newline", "offline_public_socket"} {
		t.Run(mode, func(t *testing.T) {
			in := fixtureInput(t, binary)
			in.Profile.Environments.StartArgv = append(in.Profile.Environments.StartArgv, "-test.run=^TestCandidateHarness$", "-candidate-harness="+mode)
			in.Profile.Environments.GatewayEntry = ""
			in.Profile.Environments.OfflineReadiness = offlineLabDeclaration()
			in.Target.Source.ProfileDigest = Digest(in.Profile)
			in.Target.Files.Files = []vault.File{dataFile("state", "memory/required", "memory", "selected point")}
			in.OpenData = func(context.Context, identity.SnapshotRef, string, string) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("selected point")), nil
			}
			candidate, err := Build(context.Background(), t.TempDir(), in)
			if err != nil {
				t.Fatal(err)
			}
			check, err := isolated.CheckForRuntime(in.Profile, in.Layout.Home, in.Layout.Roots["state"], "recorded-revision")
			if err != nil {
				t.Fatal(err)
			}
			check.Timeout = 900 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			result, err := isolated.GatewayProbe(ctx, candidate.Workspace, candidate.Spec, check)
			if err != nil || !result.Completed || !result.ProcessesStopped || !result.ProcessStarted || result.ProofKind != "unix-json" {
				t.Fatal("incomplete offline test", result, err)
			}
			if result.Verified() != (mode == "offline_success") || result.HTTPResponded || result.PortListening {
				t.Fatal("offline proof was confused with HTTP, process existence or a different target", result)
			}
			if mode == "offline_success" && (!result.SocketListening || !result.OfflineResponded || !result.GatewayPassed) {
				t.Fatal("missing offline evidence", result)
			}
			if mode == "offline_public_socket" && result.SocketListening {
				t.Fatal("probe connected to a non-private socket")
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "synthetic-credential") || strings.Contains(string(encoded), "recorded-revision") {
				t.Fatal("response metadata escaped to the result")
			}
			beat := filepath.Join(candidate.Workspace.HomeDir(), ".fixture/heartbeat")
			before, err := os.ReadFile(beat)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(150 * time.Millisecond)
			after, err := os.ReadFile(beat)
			if err != nil || string(before) != string(after) {
				t.Fatal("offline candidate child survived")
			}
		})
	}
}
