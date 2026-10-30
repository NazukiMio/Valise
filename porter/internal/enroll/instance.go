package enroll

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"valise/core/enroll"
	"valise/core/valise"
)

// InstancesDir is ~/.config/valise/instances.
func InstancesDir(home string) string {
	return filepath.Join(home, ".config", "valise", "instances")
}

// ReadRecord returns nil when the instance is not enrolled.
func ReadRecord(home, id string) (*enroll.Record, error) {
	if err := enroll.ValidInstanceID(id); err != nil {
		return nil, valise.New("instance_id_invalid", err.Error(), "使用合法的实例 id")
	}
	b, err := os.ReadFile(filepath.Join(InstancesDir(home), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r enroll.Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, valise.New("instance_record_corrupt", "服务器上的实例记录无法解析："+id, "重新登记以覆盖它").WithDetail(err.Error())
	}
	return &r, nil
}

// WriteRecord writes the record atomically with mode 0600.
func WriteRecord(home string, r enroll.Record) (string, error) {
	if err := enroll.ValidInstanceID(r.InstanceID); err != nil {
		return "", valise.New("instance_id_invalid", err.Error(), "使用合法的实例 id")
	}
	dir := InstancesDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	final := filepath.Join(dir, r.InstanceID+".json")
	tmp, err := os.CreateTemp(dir, "."+r.InstanceID+".tmp-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return final, os.Rename(tmp.Name(), final)
}
