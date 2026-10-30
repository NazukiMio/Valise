package vault

import (
	"encoding/json"
	"strings"
	"testing"
)

// The example in docs/VAULT_FORMAT.md section 2 must decode into Descriptor.
func TestDescriptorMatchesDocExample(t *testing.T) {
	src := `{
  "format": "valise-vault/1",
  "vault_id": "v_01J",
  "created_at": "2026-11-01T00:00:00Z",
  "chunker": { "algo": "restic-chunker", "min": 524288, "avg": 1048576, "max": 8388608, "small_file_max": 1048576 },
  "epochs": [ { "epoch": 1, "since": "2026-11-01T00:00:00Z", "recipient_fingerprints": ["abc"] } ]
}`
	dec := json.NewDecoder(strings.NewReader(src))
	dec.DisallowUnknownFields()
	var d Descriptor
	if err := dec.Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Chunker != DefaultChunker || d.Epochs[0].Epoch != 1 {
		t.Fatalf("%+v", d)
	}
}

func TestMetaHasNoPathFields(t *testing.T) {
	b, _ := json.Marshal(Meta{})
	for _, k := range []string{"path", "root", "file\""} {
		if strings.Contains(string(b), k) {
			t.Errorf("meta contains %q: %s", k, b)
		}
	}
}
