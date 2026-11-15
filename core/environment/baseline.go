package environment

import (
	"errors"
	"time"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/vault"
)

// Source is evidence supplied by the authenticated/decrypted source reader,
// never by an untrusted request's claim. Compatibility includes classification
// and root layout. Cross-migration identity proof must be resolved upstream.
type Source struct {
	Agent         identity.AgentRef
	Snapshot      identity.SnapshotRef
	Time          time.Time
	Tier          string
	ProfileDigest string
	ProgramDigest string
	LayoutDigest  string
	Verified      bool
}

func (s Source) valid() bool {
	return s.Verified && protection.ValidateAgent(s.Agent) == nil && vault.ValidID(s.Snapshot.VaultID) && vault.ValidID(s.Snapshot.SnapshotID) &&
		!s.Time.IsZero() && s.ProfileDigest != "" && s.ProgramDigest != "" && s.LayoutDigest != "" && vault.TierCategories[s.Tier] != nil
}

func compatible(a, b Source) bool {
	return a.Agent == b.Agent && a.ProfileDigest == b.ProfileDigest && a.ProgramDigest == b.ProgramDigest && a.LayoutDigest == b.LayoutDigest
}

// SelectBaseline returns the latest compatible full snapshot no later than the
// selected point. nil means rebuild from recorded program sources, not copy the
// active environment. For full/raw targets no supplemental data is selected.
func SelectBaseline(target Source, candidates []Source) (*Source, error) {
	if !target.valid() {
		return nil, errors.New("environment: source evidence is incomplete")
	}
	if target.Tier == vault.TierFull || target.Tier == vault.TierRaw {
		return nil, nil
	}
	var best *Source
	for _, c := range candidates {
		if !c.valid() || !compatible(target, c) || c.Tier != vault.TierFull || c.Time.After(target.Time) {
			continue
		}
		if best == nil || c.Time.After(best.Time) || c.Time.Equal(best.Time) && c.Snapshot.SnapshotID > best.Snapshot.SnapshotID {
			copy := c
			best = &copy
		}
	}
	return best, nil
}
