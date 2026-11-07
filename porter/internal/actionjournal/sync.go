package actionjournal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
)

func TreeDigest(root string) (string, error) { return TreeDigestAllow(root, nil) }

// TreeDigestAllow is TreeDigest accepting a plan's handled links.
func TreeDigestAllow(root string, allow map[string]string) (string, error) {
	facts, _, err := TreeFactsAllow(root, allow)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// SyncTree makes successful configuration writes durable before publishing.
func SyncTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		err = f.Sync()
		ce := f.Close()
		if err != nil {
			return err
		}
		return ce
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err = syncDir(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}
