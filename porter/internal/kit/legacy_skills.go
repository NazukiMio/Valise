package kit

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

var legacySkillOrder = []string{
	"valise-enroll",
	"valise-upkeep",
	"valise-handoff",
	"valise-checkup",
}

// SHA-256 digests of every historically shipped SKILL.md from the retired
// four-skill kit so install/uninstall can safely remove unmodified files while
// preserving user-edited copies.
var legacySkillDigests = map[string]map[string]bool{
	"valise-enroll": {
		"efe94315408a43d817ad3e2d4cc09bf4afc368868c1387f0a21793d42aee0f84": true,
		"ef9f4ac62737b8a2d42a7e8d7f77d0603d29cb0d1ab385f0e14a9ab918a4d554": true,
		"c61c0e2cf1dcf1805b430db43df747cf856db07623d5e16bee89b54b9ab18cc4": true,
		"d42440baad554dd4d5d9688d6e13e1ffaeed758f2f818f0960e7861e4557d431": true,
		"3308af8db3fd4dae36b2028d512a04a480d68034e4daff4bcf0d55debaa60d2d": true,
	},
	"valise-upkeep": {
		"a36d5f24d7ddd8c34e679342f1929888896b3cadf93228b5b2d342443690ed55": true,
		"0a74c9f653767680369ead59fe46985bdd4777b10c4e568e3798fa28da1519cf": true,
		"adfc8c8303872908ccc1a2aede4495795b6bc82df35ca871ed443317cdcc21fa": true,
		"7ac034f0a8d38198c1c0e544f8e3c995025e63b010974aa7b69c19aaa6dbc0d0": true,
	},
	"valise-handoff": {
		"be11d3920f2cbf65084e08da6c737924cfbe92bcaf0e3c42059589e3d14f7d27": true,
		"aeda49305345cad55276be21ed25f338213c33d1778e0eb0d26221bf151f0dc9": true,
	},
	"valise-checkup": {
		"727af5bc25c9d50e183fcf14c653c075c705a06c8972b90953e31393381e8e7b": true,
		"b620cc78fd2bb2dad647555e0baf640131c5415c16869375223b98bf88324e20": true,
		"fa577e6a9f5ace2ba0da85e3fc72772635a39ed15bd206700c54dad35fc7cd81": true,
	},
}

func isUnmodifiedLegacySkill(skill string, content []byte) bool {
	digests := legacySkillDigests[skill]
	if len(digests) == 0 {
		return false
	}
	sum := sha256.Sum256(content)
	return digests[hex.EncodeToString(sum[:])]
}

func cleanLegacySkills(dir string, skip map[string]bool, force, dryRun bool) (removed, kept []string, err error) {
	rmFile := func(p string) error {
		if !dryRun {
			if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		removed = append(removed, p)
		return nil
	}
	for _, skill := range legacySkillOrder {
		if skip[skill] {
			continue
		}
		sdir := filepath.Join(dir, skill)
		spath := filepath.Join(sdir, "SKILL.md")
		b, readErr := os.ReadFile(spath)
		switch {
		case readErr == nil:
			if !force && !isUnmodifiedLegacySkill(skill, b) {
				kept = append(kept, spath)
				continue
			}
			if e := rmFile(spath); e != nil {
				return removed, kept, e
			}
			for _, bak := range backups(sdir) {
				if e := rmFile(bak); e != nil {
					return removed, kept, e
				}
			}
		case os.IsNotExist(readErr):
			for _, bak := range backups(sdir) {
				if e := rmFile(bak); e != nil {
					return removed, kept, e
				}
			}
		default:
			return removed, kept, readErr
		}
		if ents, e := os.ReadDir(sdir); e == nil && !dryRun && len(ents) == 0 {
			_ = os.Remove(sdir)
		}
	}
	return removed, kept, nil
}
