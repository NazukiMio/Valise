package revive

import (
	"valise/core/identity"
	"valise/porter/internal/activationrecord"
)

// Aliases retain the private format and its digest while allowing daily
// runtime readers to share the exact same validation without importing an
// executor or triggering the sealing/activation package dependency cycle.
type ActivationPlan = activationrecord.Plan
type ActivationFact = activationrecord.Fact
type ActivationRecord = activationrecord.Record

const activationSchema = activationrecord.Schema

func activationDigest(r *ActivationRecord) string { return activationrecord.Digest(r) }
func validActivation(r *ActivationRecord, agent identity.AgentRef, op string) bool {
	return activationrecord.Valid(r, agent, op)
}
func activationDir(home string, agent identity.AgentRef, create bool) (string, error) {
	return activationrecord.Dir(home, agent, create)
}
func loadActivation(home string, agent identity.AgentRef, op string) (*ActivationRecord, error) {
	return activationrecord.Load(home, agent, op)
}
func saveActivation(home string, r *ActivationRecord) error { return activationrecord.Save(home, r) }
