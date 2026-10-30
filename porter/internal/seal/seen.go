package seal

import (
	"encoding/json"
	"os"
	"path/filepath"

	"valise/core/vault"
)

// stableCategories are expected not to change between seals; a file in
// them that changes every time is probably runtime state (a hint for the
// agent and the user, docs/AGENT_KIT.md section 3.1).
var stableCategories = map[string]bool{"identity": true, "config": true, "skills": true}

// SeenEntry is the change history of one file.
type SeenEntry struct {
	SHA256 string `json:"sha256"`
	// Streak counts consecutive seals in which the file changed.
	Streak int `json:"streak"`
	Seals  int `json:"seals"`
}

// SeenPath is ~/.local/state/valise/seen/<instance_id>.json. It holds paths
// and hashes only, never contents.
func (p Paths) SeenPath(instance string) string {
	return filepath.Join(p.State(), "seen", instance+".json")
}

// ReadSeen returns the change statistics of an instance.
func ReadSeen(p Paths, instance string) map[string]SeenEntry {
	seen := map[string]SeenEntry{}
	if b, err := os.ReadFile(p.SeenPath(instance)); err == nil {
		json.Unmarshal(b, &seen)
	}
	return seen
}

func updateSeen(p Paths, instance string, files []vault.File) {
	old := ReadSeen(p, instance)
	next := map[string]SeenEntry{}
	for _, f := range files {
		if !stableCategories[f.Category] {
			continue
		}
		key := f.Root + ":" + f.Path
		e, had := old[key]
		switch {
		case !had:
			e = SeenEntry{SHA256: f.SHA256}
		case e.SHA256 != f.SHA256:
			e.SHA256, e.Streak = f.SHA256, e.Streak+1
		default:
			e.Streak = 0
		}
		e.Seals++
		next[key] = e
	}
	b, _ := json.MarshalIndent(next, "", "  ")
	writeAtomic(p.SeenPath(instance), b, 0o600)
}
