package service

import (
	"valise/core/enroll"
	"valise/core/profile"
)

// Transport support does not make every profile/version/source rebuildable.
// Snapshot-specific encrypted evidence is still checked after key unlock.
func (s *Service) environmentTestSupported(in enroll.InstanceSummary) bool {
	set, err := s.Profiles()
	if err != nil || set.ByID[in.ProfileID] == nil || set.ByID[in.ProfileID].Effective == nil {
		return false
	}
	return declaredEnvironmentTest(*set.ByID[in.ProfileID].Effective, in)
}

func declaredEnvironmentTest(p profile.Profile, in enroll.InstanceSummary) bool {
	return in.Name == "" && p.ID == in.ProfileID && p.ProfileVersion == in.ProfileVersion && p.Environments != nil &&
		p.Environments.Engine == "linux-bwrap" && p.Provenance != nil && p.Provenance.Kind == profile.ProvenanceNPMGlobal
}
