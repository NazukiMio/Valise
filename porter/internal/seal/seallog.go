package seal

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"

	"valise/core/enroll"
	"valise/core/valise"
	"valise/core/vaultrpc"
)

// SealLog lists an instance's successful seals from the audit log: who
// started each and why. The audit log holds no file contents.
func SealLog(p Paths, instance string) (*vaultrpc.SealLogResult, error) {
	if err := enroll.ValidInstanceID(instance); err != nil {
		return nil, valise.New("instance_invalid", err.Error(), "检查实例名")
	}
	res := &vaultrpc.SealLogResult{Entries: []vaultrpc.SealLogEntry{}}
	f, err := os.Open(p.Audit())
	if errors.Is(err, os.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e AuditEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Op != "seal" || e.Result != "ok" || e.Instance != instance || e.SnapshotID == "" {
			continue
		}
		res.Entries = append(res.Entries, vaultrpc.SealLogEntry{SnapshotID: e.SnapshotID, Time: e.Time, Initiator: e.Initiator, Reason: e.Reason})
	}
	return res, sc.Err()
}
