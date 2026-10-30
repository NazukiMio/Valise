package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// Problem is one verification failure. It never contains file contents.
type Problem struct {
	Where string `json:"where"`
	What  string `json:"what"`
}

func (p Problem) String() string { return p.Where + ": " + p.What }

// VerifyNoKey checks what can be checked without the private key
// (docs/VAULT_FORMAT.md section 7).
func VerifyNoKey(dir string) ([]Problem, error) {
	v, err := Open(dir)
	if err != nil {
		return nil, err
	}
	var probs []Problem
	add := func(where, format string, a ...any) { probs = append(probs, Problem{where, fmt.Sprintf(format, a...)}) }

	sizes := map[string]int64{}
	packsDir := joinRel(dir, PacksDir)
	subs, _ := os.ReadDir(packsDir)
	for _, sub := range subs {
		if !sub.IsDir() {
			continue
		}
		ents, _ := os.ReadDir(filepath.Join(packsDir, sub.Name()))
		for _, e := range ents {
			id, err := packIDFromName(e.Name())
			where := "packs/" + sub.Name() + "/" + e.Name()
			if err != nil || !strings.HasPrefix(id, sub.Name()) {
				add(where, "文件名不符合格式")
				continue
			}
			f, err := os.Open(filepath.Join(packsDir, sub.Name(), e.Name()))
			if err != nil {
				add(where, "无法读取")
				continue
			}
			h := sha256.New()
			n, err := io.Copy(h, f)
			f.Close()
			if err != nil {
				add(where, "无法读取")
				continue
			}
			if hex.EncodeToString(h.Sum(nil)) != id {
				add(where, "SHA-256 与 pack_id 不一致（文件被篡改或损坏）")
			}
			sizes[id] = n
		}
	}
	for id, e := range v.blobs {
		size, ok := sizes[e.Pack]
		switch {
		case !ok:
			add("blob "+short(id), "所在的 pack %s 不存在", short(e.Pack))
		case e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > size:
			add("blob "+short(id), "offset/length 超出 pack 范围")
		}
	}
	epochs := map[int]bool{}
	for _, ep := range v.Epochs {
		epochs[ep.Epoch] = true
	}
	for _, m := range v.Metas {
		if _, err := os.Stat(joinRel(dir, SnapshotPath(m.SnapshotID))); err != nil {
			add("snapshot "+m.SnapshotID, "缺少加密的快照文件")
		}
		if _, err := os.Stat(joinRel(dir, IndexPath(m.SnapshotID))); err != nil {
			add("snapshot "+m.SnapshotID, "缺少 index 文件")
		}
		if !epochs[m.Epoch] {
			add("snapshot "+m.SnapshotID, "epoch %d 没有对应的 epochs 文件", m.Epoch)
		}
		if _, err := os.Stat(joinRel(dir, DedupeKeyPath(m.Epoch))); err != nil {
			add("snapshot "+m.SnapshotID, "epoch %d 没有对应的去重参数文件", m.Epoch)
		}
	}
	return probs, nil
}

// VerifyWithKey decrypts every listed snapshot (all when ids is empty) and
// checks every file's SHA-256.
func VerifyWithKey(dir string, snapshotIDs []string, ids ...age.Identity) (files int, probs []Problem, err error) {
	v, err := Open(dir)
	if err != nil {
		return 0, nil, err
	}
	if len(snapshotIDs) == 0 {
		for _, m := range v.Metas {
			snapshotIDs = append(snapshotIDs, m.SnapshotID)
		}
	}
	for _, sid := range snapshotIDs {
		_, list, err := v.Snapshot(sid, ids...)
		if err != nil {
			probs = append(probs, Problem{"snapshot " + sid, err.Error()})
			continue
		}
		for _, f := range list.Files {
			files++
			if err := v.WriteFile(f, io.Discard, ids...); err != nil {
				probs = append(probs, Problem{"snapshot " + sid, err.Error()})
			}
		}
	}
	return files, probs, nil
}
