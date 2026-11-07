package actiontxn

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	porterenroll "valise/porter/internal/enroll"
)

// Re-registration must not turn a retained directory into a cleanup target.
func (r Runner) checkCleanupRoots(paths []string) error {
	records, err := porterenroll.ListRecords(r.Env.Home)
	if err != nil {
		return err
	}
	for _, rec := range records {
		for _, root := range rec.Roots {
			resolved, err := filepath.EvalSymlinks(root)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return reject("无法核实当前登记目录，不能清理")
			}
			for _, target := range paths {
				if containsPath(target, resolved) || containsPath(resolved, target) {
					return reject("待清理目录与当前登记目录重叠，保留现场")
				}
			}
		}
	}
	return nil
}
func containsPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
