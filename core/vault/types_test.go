package vault

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The example in docs/VAULT_FORMAT.md section 2 must decode into Descriptor.
func TestDescriptorMatchesDocExample(t *testing.T) {
	src := `{
  "format": "valise-vault/1",
  "vault_id": "v_01JBX7Q2W8M3ZK5N6P9R4T0Y2A",
  "created_at": "2026-11-01T00:00:00Z",
  "chunker": { "algo": "restic-chunker", "min": 524288, "avg": 1048576, "max": 8388608, "small_file_max": 1048576 }
}`
	dec := json.NewDecoder(strings.NewReader(src))
	dec.DisallowUnknownFields()
	var d Descriptor
	if err := dec.Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Chunker != DefaultChunker || !ValidID(d.VaultID) {
		t.Fatalf("%+v", d)
	}
}

func TestMetaHasNoPathFields(t *testing.T) {
	b, _ := json.Marshal(Meta{})
	for _, k := range []string{"path", "root", "\"file\""} {
		if strings.Contains(string(b), k) {
			t.Errorf("meta contains %q: %s", k, b)
		}
	}
}

func TestNewID(t *testing.T) {
	a := NewID("s", time.UnixMilli(1000))
	b := NewID("s", time.UnixMilli(2000))
	if !ValidID(a) || !ValidID(b) || a >= b || len(a) != 28 {
		t.Fatalf("%s %s", a, b)
	}
	// The time prefix of 0 ms is all zeros.
	if z := NewID("v", time.UnixMilli(0)); !strings.HasPrefix(z, "v_0000000000") {
		t.Fatal(z)
	}
}

func TestValidRel(t *testing.T) {
	id := NewID("s", time.Now())
	pack := strings.Repeat("ab", 32)
	for _, ok := range []string{"VAULT", "epochs/1.json", "keys/dedupe.12.age", "packs/ab/" + pack + ".pack",
		"index/" + id + ".json", "snapshots/" + id + ".age", "snapshots/" + id + ".meta.json"} {
		if !ValidRel(ok) {
			t.Errorf("%s rejected", ok)
		}
	}
	for _, bad := range []string{"", "../VAULT", "/etc/passwd", "packs/tmp-123", "epochs/0.json", "index/../VAULT",
		"snapshots/.tmp-1", "keys/dedupe.1.age/../../x"} {
		if ValidRel(bad) {
			t.Errorf("%s accepted", bad)
		}
	}
}
