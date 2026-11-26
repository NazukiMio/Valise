package kit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"valise/core/agentkit"
)

// maxDiskContent caps the skill text returned for a modified skill.
const maxDiskContent = 64 << 10

// officialActiveSkillDigests lists SHA-256 digests of earlier shipped
// versions of each skill; a disk file matching one is an official update
// waiting, not a user edit.
var officialActiveSkillDigests = map[string]map[string]bool{
	"valise-onboard": {
		"679662ebc27dbeb207653e1e8fc969ba8602acea8af45f45075054447b3e467f": true,
		"9a77245abd277c3923c7efa228e0bd1414599755473f15864bb4a19b658816db": true,
		"47fa9412f2df51dafe6cf1106f16ce9f123b991262fc575a281870403cccb935": true,
		"75032a8e188e7931a2407559751d7fdffb565c8acb3587ad0d21fb0641b653e1": true,
	},
	"valise-backup": {
		"9018948036fe4835e8b1163979c2342a8baa97632e9fc872da40c4d14ece6aac": true,
		"df563c518bad6cedfa3c5cb5cfd20d9275cb90258100bde0986e804006a9c334": true,
	},
}

// skillInstall is one installed-skill baseline.
type skillInstall struct {
	SHA256      string    `json:"sha256"`
	InstalledAt time.Time `json:"installed_at"`
}

type skillManifest struct {
	Skills map[string]skillInstall `json:"skills"`
}

func manifestPath(home, instance string) string {
	return filepath.Join(home, ".local", "state", "valise", "kit-skills-"+instance+".json")
}

func loadManifest(home, instance string) *skillManifest {
	m := &skillManifest{}
	if b, err := os.ReadFile(manifestPath(home, instance)); err == nil {
		if json.Unmarshal(b, m) != nil {
			m = &skillManifest{}
		}
	}
	if m.Skills == nil {
		m.Skills = map[string]skillInstall{}
	}
	return m
}

func saveManifest(home, instance string, m *skillManifest) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return writeAtomic(manifestPath(home, instance), b)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// isOfficialShippedSkill reports whether diskSHA equals the currently shipped
// skill content, the manifest baseline installed by Valise, or any historical
// shipped official digest of skill.
func isOfficialShippedSkill(skill, diskSHA string, shipped []byte, installedSHA string) bool {
	return sha256Hex(shipped) == diskSHA || (installedSHA != "" && installedSHA == diskSHA) || officialActiveSkillDigests[skill][diskSHA]
}

// classifySkill decides the four-way change kind of a skill whose disk
// content differs from the shipped one.
func classifySkill(skill, diskSHA, shippedSHA string, base skillInstall) string {
	if officialActiveSkillDigests[skill][diskSHA] {
		return agentkit.SkillChangeOfficialUpdate
	}
	baseDiffers := base.SHA256 != "" && base.SHA256 != shippedSHA
	if baseDiffers && diskSHA == base.SHA256 {
		return agentkit.SkillChangeOfficialUpdate
	}
	if base.SHA256 == shippedSHA {
		return agentkit.SkillChangeLocalModified
	}
	if baseDiffers {
		return agentkit.SkillChangeBothChanged
	}
	return agentkit.SkillChangeLocalModified
}

func boundedUTF8(b []byte) string {
	if len(b) > maxDiskContent || !utf8.Valid(b) {
		return ""
	}
	return string(b)
}
