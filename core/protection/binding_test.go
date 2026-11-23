package protection

import (
	"encoding/json"
	"strings"
	"testing"

	"valise/core/identity"
)

func TestBindingRejectsUnknownAndIncompleteChoices(t *testing.T) {
	var copy LocalCopy
	if err := json.Unmarshal([]byte(`{"repo_id":"disk","after_backup":true,"on_client_open":false,"copy_secrets":true}`), &copy); err == nil {
		t.Fatal("unknown copy choice accepted")
	}
	if err := ValidateLocalCopies(nil); err == nil {
		t.Fatal("missing full local choices accepted")
	}
	enabled := true
	good := LocalCopy{RepoID: "disk", AfterBackup: &enabled, OnClientOpen: &enabled}
	for _, copies := range [][]LocalCopy{{{RepoID: "disk"}}, {good, good}} {
		if err := ValidateLocalCopies(copies); err == nil {
			t.Fatal("incomplete/duplicate copy accepted")
		}
	}
	if err := ValidateLocalCopies([]LocalCopy{}); err != nil {
		t.Fatal(err)
	}
}

func TestBindingRevisionRoundtripAndIdentity(t *testing.T) {
	clientID := strings.Repeat("a", 32)
	enabled := false
	binding := ClientBinding{ClientID: clientID, AgentRef: identity.AgentRef{ServerUID: "server", InstallID: "sample", InstanceID: "sample", Generation: "generation"}, Revision: Revision(9007199254740993), ServerRevisionSeen: 2, LocalCopies: []LocalCopy{{RepoID: "disk", AfterBackup: &enabled, OnClientOpen: &enabled}}}
	b, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"revision":"9007199254740993"`) {
		t.Fatalf("lossy revision %s", b)
	}
	var restored ClientBinding
	if err = json.Unmarshal(b, &restored); err != nil || restored.Revision != binding.Revision {
		t.Fatalf("roundtrip: %+v %v", restored, err)
	}
	file := BindingFile{SchemaVersion: BindingSchema, ClientID: clientID, Bindings: []ClientBinding{binding}}
	if err = file.Validate(); err != nil {
		t.Fatal(err)
	}
	file.Bindings = append(file.Bindings, binding)
	if err = file.Validate(); err == nil {
		t.Fatal("duplicate AgentRef accepted")
	}
	file.Bindings = file.Bindings[:1]
	file.ClientID = strings.Repeat("b", 32)
	if err = file.Validate(); err == nil {
		t.Fatal("foreign client binding accepted")
	}
	// CloudCopies optional: nil is valid, non-nil validates duplicate/missing choices.
	binding.CloudCopies = []LocalCopy{{RepoID: "cloud-1", AfterBackup: &enabled, OnClientOpen: &enabled}}
	if err := binding.Validate(); err != nil {
		t.Fatal("valid CloudCopies rejected:", err)
	}
	binding.CloudCopies = []LocalCopy{{RepoID: "cloud-1"}}
	if err := binding.Validate(); err == nil {
		t.Fatal("incomplete CloudCopies accepted")
	}
}
