package revive

import (
	"strings"

	"valise/core/vault"
	"valise/porter/internal/environments"
)

// The stage and current Agent already occupy disk. Reserve the independent
// tar, unpacked programs and data together, plus conservative metadata/block
// padding. Check after observing the exact program list, before copying bytes.
func checkTestSpace(parent string, program environments.ProgramMaterial, data vault.Files) error {
	var need uint64 = 1 << 20
	add := func(size int64, path string, copies uint64) bool {
		if size < 0 || size > 16<<30 {
			return false
		}
		n := uint64(size)*copies + uint64(strings.Count(path, "/")+2)*8192 + 4096
		if n > 64<<30 || need > 64<<30-n {
			return false
		}
		need += n
		return true
	}
	for _, f := range program.Files {
		if !add(f.Size, f.Path, 2) {
			return testFailure("environment_space_low")
		}
	}
	for _, f := range data.Files {
		if !add(f.Size, f.Path, 1) {
			return testFailure("environment_space_low")
		}
	}
	for _, l := range data.Symlinks {
		if !add(0, l.Path, 1) {
			return testFailure("environment_space_low")
		}
	}
	free, err := statfsFree(parent)
	if err != nil || free < need {
		return testFailure("environment_space_low")
	}
	return nil
}
