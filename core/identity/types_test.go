package identity

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestAgentRefWireContract(t *testing.T) {
	ref := AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "instance", Generation: "generation"}
	b, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"server_uid":"server","install_id":"install","instance_id":"instance","generation":"generation"}`
	if string(b) != want {
		t.Fatalf("wire shape changed: %s", b)
	}
	var roundtrip AgentRef
	if err := json.Unmarshal(b, &roundtrip); err != nil || roundtrip != ref {
		t.Fatalf("%+v %v", roundtrip, err)
	}
}

func TestNewID128Bit(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatal(err)
		}
		b, err := hex.DecodeString(id)
		if err != nil || len(b) != 16 || seen[id] {
			t.Fatalf("invalid or repeated identifier: %q %v", id, err)
		}
		seen[id] = true
	}
}
