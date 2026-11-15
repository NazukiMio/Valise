package enroll

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"valise/core/enroll"
)

// ListSavedRecords is a read-only scope inventory, including old records that
// have not been imported. An accepted dossier takes precedence over its old
// copy. This function never migrates, renames, or repairs metadata.
func ListSavedRecords(home string) ([]enroll.Record, error) {
	records, err := ListAcceptedRecords(home)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, rec := range records {
		if enroll.ValidInstanceID(rec.InstanceID) != nil || seen[rec.InstanceID] || len(rec.Roots) == 0 {
			return nil, errors.New("invalid accepted record scope")
		}
		seen[rec.InstanceID] = true
	}
	entries, err := os.ReadDir(InstancesDir(home))
	if os.IsNotExist(err) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || strings.Contains(id, ".") || enroll.ValidInstanceID(id) != nil || seen[id] {
			continue
		}
		f, err := os.OpenFile(filepath.Join(InstancesDir(home), entry.Name()), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
			f.Close()
			return nil, errors.New("invalid legacy record scope")
		}
		b, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		f.Close()
		var rec enroll.Record
		if err != nil || len(b) > 2<<20 || json.Unmarshal(b, &rec) != nil || rec.InstanceID != id || enroll.ValidInstanceID(rec.ProfileID) != nil || len(rec.Roots) == 0 {
			return nil, errors.New("invalid legacy record scope")
		}
		records = append(records, rec)
	}
	return records, nil
}
