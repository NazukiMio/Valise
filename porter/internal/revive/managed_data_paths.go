package revive

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"valise/core/revive"
)

func managedRootIdentity(id, path string) (revive.ManagedRoot, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return revive.ManagedRoot{}, err
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || stat.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0022 != 0 {
		return revive.ManagedRoot{}, managedDataError()
	}
	return revive.ManagedRoot{ID: id, Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func checkManagedLocations(j *revive.Journal) error {
	m := j.ManagedData
	if m == nil || len(j.Roots) == 0 || len(m.OriginalRoots) != len(j.Roots) || len(m.CandidateRoots) != len(j.Roots) {
		return managedDataError()
	}
	for i, root := range j.Roots {
		a, b := m.OriginalRoots[i], m.CandidateRoots[i]
		if a.ID != root.ID || b.ID != root.ID || a == b || !root.LiveExisted {
			return managedDataError()
		}
		original, candidate := 0, 0
		for _, path := range []string{root.Live, root.Prev, root.Stage, root.Failed} {
			node, err := managedRootIdentity(root.ID, path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			switch {
			case node == a && (path == root.Live || path == root.Prev):
				original++
			case node == b && path != root.Prev:
				candidate++
			default:
				return managedDataError()
			}
		}
		if original != 1 || candidate != 1 {
			return managedDataError()
		}
	}
	return nil
}

// The legacy stage manifest describes regular files only. The first managed
// data adapter rejects links/special nodes rather than silently carrying an
// unbound link into the formal environment. A future link manifest must bind
// and validate every target before this restriction is lifted.
func checkManagedStage(j *revive.Journal) error {
	for _, root := range j.Roots {
		err := filepath.WalkDir(root.Stage, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			st, err := d.Info()
			if err != nil {
				return err
			}
			stat, ok := st.Sys().(*syscall.Stat_t)
			if !ok || stat.Uid != uint32(os.Getuid()) || (!st.IsDir() && (!st.Mode().IsRegular() || stat.Nlink != 1)) {
				return managedDataError()
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func renameData(j *revive.Journal, from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	if j.ManagedData == nil {
		return nil
	}
	return syncDataDirs(filepath.Dir(from), filepath.Dir(to))
}

func syncDataDirs(paths ...string) error {
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		d, err := os.Open(path)
		if err != nil {
			return err
		}
		err = d.Sync()
		d.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
