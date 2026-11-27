package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"time"

	"filippo.io/age"

	"valise/core/rpc"
	"valise/core/valise"
	"valise/core/vaultrpc"
	"valise/porter/internal/seal"
)

// The owner proof (docs/ARCHITECTURE.md 2.2): a nonce encrypted with age to
// the server's recipients; only the holder of the Valise passphrase can
// open it. A proved daily session may run owner methods.

func init() {
	methods[rpc.MethodOwnerChallenge] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		rs, err := seal.ReadRecipients(seal.Paths{Home: s.cfg.Home})
		if err != nil {
			return nil, err
		}
		if len(rs) == 0 {
			return nil, valise.New("owner_no_recipient", "这台服务器还没有登记加密钥匙，无法确认是你本人",
				"先登记你的钥匙：valise key register <服务器>")
		}
		var to []age.Recipient
		for _, r := range rs {
			rc, err := age.ParseX25519Recipient(r)
			if err != nil {
				return nil, err
			}
			to = append(to, rc)
		}
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return nil, err
		}
		var ct bytes.Buffer
		w, err := age.Encrypt(&ct, to...)
		if err != nil {
			return nil, err
		}
		w.Write(nonce)
		if err := w.Close(); err != nil {
			return nil, err
		}
		s.challenge = nonce
		return rpc.OwnerChallengeResult{Ciphertext: ct.Bytes()}, nil
	}
	methods[rpc.MethodOwnerProve] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p rpc.OwnerProveParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		ok := len(s.challenge) > 0 && subtle.ConstantTimeCompare(p.Nonce, s.challenge) == 1
		s.challenge = nil // one attempt per challenge
		if !ok {
			return nil, valise.New("owner_proof_wrong", "没能确认是你本人：解开的内容不对", "确认用的是登记在这台服务器上的那把钥匙")
		}
		s.owner = true
		return struct{}{}, nil
	}
}

// require checks a conditional method's tier, picked by its handler.
func (s *session) require(t rpc.Tier, method string) error {
	if !s.daily() {
		return nil
	}
	switch t {
	case rpc.TierDaily:
		return nil
	case rpc.TierOwner:
		if s.owner {
			return nil
		}
		return rpc.OwnerRequired(method)
	}
	return rpc.AdminRequired(method)
}

// sensitiveAge: deleting an operation's leftovers after a week is not
// sensitive.
const sensitiveAge = 7 * 24 * time.Hour

// leafFoundElsewhere reports whether a server period may be deleted at the
// owner tier: it has ended and every escape pod in it reached a cloud
// backup (docs/ARCHITECTURE.md 2.2).
func leafFoundElsewhere(st *vaultrpc.StatusResult, leaf string) bool {
	synced := map[string]bool{}
	for _, t := range st.Targets {
		for _, id := range t.LastSynced {
			synced[id] = true
		}
	}
	for _, l := range st.Leaves {
		if l.Leaf != leaf {
			continue
		}
		if l.Current {
			return false
		}
		for _, id := range l.Snapshots {
			if !synced[id] {
				return false
			}
		}
		return true
	}
	return false
}

// leafOnServer reports whether the server still holds a period, so its
// copy on a cloud backup may go at the owner tier.
func leafOnServer(st *vaultrpc.StatusResult, leaf string) bool {
	for _, l := range st.Leaves {
		if l.Leaf == leaf && !l.Deleting && l.VaultID != "" && len(l.Snapshots) > 0 {
			return true
		}
	}
	return false
}

// snapshotsFoundElsewhere reports whether every listed snapshot is recorded
// on a copy location, so deleting them from the server may go at the owner
// tier; anything else is developer mode.
func snapshotsFoundElsewhere(st *vaultrpc.StatusResult, ids []string) bool {
	synced := map[string]bool{}
	for _, t := range st.Targets {
		for _, id := range t.LastSynced {
			synced[id] = true
		}
	}
	for _, id := range ids {
		if !synced[id] {
			return false
		}
	}
	return len(ids) > 0
}

// snapshotsOnServer reports whether the server still holds every listed
// snapshot, so their copies on a cloud backup may go at the owner tier.
func snapshotsOnServer(st *vaultrpc.StatusResult, ids []string) bool {
	have := map[string]bool{}
	for _, m := range st.Snapshots {
		have[m.SnapshotID] = true
	}
	for _, id := range ids {
		if !have[id] {
			return false
		}
	}
	return len(ids) > 0
}
