package enroll

import (
	"context"
	"reflect"

	model "valise/core/identity"
	"valise/core/valise"
	identitystore "valise/porter/internal/identity"
)

// ValidateIdentity checks an exact accepted generation and its current facts.
// It does not initialize identities or accept changed profiles or directories.
func ValidateIdentity(ctx context.Context, home string, ref model.AgentRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := identitystore.Read(home, ref.InstanceID)
	if err != nil {
		return err
	}
	if b.IdentityState != model.Bound || b.Agent == nil {
		return valise.New("identity_unbound", "实例身份尚未绑定或需要核实", "明确重新接入实例后再准备计划")
	}
	mismatch := func() error {
		return valise.New("identity_conflict", "当前实例世代或档案与已接受的身份不一致", "核实实例目录和资料版本后明确重新接入，并重新准备计划")
	}
	if b.Agent.Ref != ref {
		return mismatch()
	}
	r, err := ReadRecord(home, ref.InstanceID)
	if err != nil {
		return err
	}
	if r == nil || r.Superseded != nil || !reflect.DeepEqual(r.Roots, b.Agent.Roots) || InstallOf(home, ref.InstanceID) != ref.InstallID {
		return mismatch()
	}
	p, err := readDossierProfile(home, ref.InstallID)
	if err != nil {
		return err
	}
	if p.ID != ref.InstallID || p.ID != r.ProfileID || p.ProfileVersion != r.ProfileVersion {
		return mismatch()
	}
	digest, err := identitystore.ProfileDigest(*p)
	if err != nil {
		return err
	}
	if digest != b.Agent.ProfileDigest {
		if r.Name == "" {
			return mismatch()
		}
		subDigest, err := identitystore.ProfileDigest(p.ForAgent(r.Name))
		if err != nil {
			return err
		}
		if subDigest != b.Agent.ProfileDigest {
			return mismatch()
		}
	}
	return ctx.Err()
}
