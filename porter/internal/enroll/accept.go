package enroll

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"valise/core/enroll"
	model "valise/core/identity"
	"valise/core/profile"
	"valise/core/valise"
	identitystore "valise/porter/internal/identity"
)

// AcceptRecord commits explicit user acceptance with its independent identity.
// Ordinary WriteRecord calls (including recovery facts) do not create identities.
func AcceptRecord(home string, r enroll.Record, p profile.Profile) (path string, agent *model.Agent, err error) {
	if e := enroll.ValidInstanceID(r.InstanceID); e != nil {
		return "", nil, e
	}
	if p.ID == "" || p.ProfileVersion != r.ProfileVersion || p.ID != r.ProfileID {
		return "", nil, valise.New("instance_profile_mismatch", "实例记录与 profile 版本不一致", "重新接入")
	}
	if e := enroll.ValidInstanceID(p.ID); e != nil {
		return "", nil, e
	}
	facts := r
	facts.EnrolledAt = time.Time{}
	rb, err := json.Marshal(facts)
	if err != nil {
		return "", nil, err
	}
	rd := sha256.Sum256(rb)
	err = identitystore.WithLock(home, func() error {
		effProf := EffectiveDossierProfile(home, r, p)
		pdHex, e := identitystore.ProfileDigest(effProf)
		if e != nil {
			return e
		}
		old, e := ReadRecord(home, r.InstanceID)
		if e != nil {
			return e
		}
		in := identitystore.AcceptInput{InstallID: p.ID, InstanceID: r.InstanceID, Roots: r.Roots,
			ProfileDigest: pdHex, FactsDigest: hex.EncodeToString(rd[:]), Existing: old != nil,
			Superseded: old != nil && old.Superseded != nil}
		agent, e = identitystore.AcceptLocked(home, in, func() error {
			var e error
			path, e = WriteRecord(home, r, p)
			if e != nil {
				return e
			}
			// Both renamed facts and their directory entry must be durable before
			// the independent identity commit can be published as complete.
			for _, dir := range []string{DossiersDir(home), filepath.Dir(DossiersDir(home))} {
				d, err := os.Open(dir)
				if err != nil {
					return err
				}
				err = d.Sync()
				closeErr := d.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
			return nil
		})
		return e
	})
	return
}
