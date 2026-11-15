package revive

import (
	"context"

	"valise/porter/internal/activationrecord"
	"valise/porter/internal/environments"
)

// The production preparer binds native source material in addition to the
// private programme. Interpreter/PID checks alone miss in-place JS updates.
// Old internal fixture records without this source binding keep their reader.
func (a *ActivationRunner) verifyNativeSource(ctx context.Context, rec *ActivationRecord) error {
	if rec.Plan.ManagedSourceDigest != "" {
		source, err := activationrecord.LoadSource(a.home(), rec.Plan.Agent, rec.Plan.OperationID)
		if err != nil || environments.Digest(source) != rec.Plan.ManagedSourceDigest || source.Original != rec.Plan.Original || source.Definition != rec.OriginalDefinition || source.Materials != rec.Plan.OriginalMaterials {
			return environments.ErrSource
		}
		if source.RetiredNative != nil {
			if source.RetiredNative.Validate(rec.Plan.OriginalProfile) != nil {
				return environments.ErrSource
			}
			native, err := a.runtime.ReadNative(ctx, *source.RetiredNative)
			if err != nil || !native.Stopped || native.PendingJob || native.UnitFileState != "disabled" {
				return environments.ErrSource
			}
		}
	}
	if rec.Plan.NativeSourceMaterials == "" {
		return nil
	}
	programs := environments.ProgramStore{Home: a.home()}
	b, err := programs.Load(rec.Plan.Agent, rec.Plan.Original)
	if err != nil {
		return environments.ErrSource
	}
	// readOriginal follows this check immediately and revalidates frozen PID,
	// executable and startup policy. No source recheck is needed after native A
	// is down: failure return uses its already independent verified programme.
	return environments.VerifyOriginalSource(ctx, b, rec.Plan.NativeSourceMaterials)
}
