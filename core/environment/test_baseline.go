package environment

import (
	"encoding/json"
	"reflect"
	"slices"

	"valise/core/vault"
)

// CompatibleTestBaseline checks encrypted source facts only. Callers must also
// authenticate both snapshot references and their exact Agent generation.
func CompatibleTestBaseline(target, base vault.Snapshot) bool {
	if target.Tier == vault.TierFull || target.Tier == vault.TierRaw || base.Tier != vault.TierFull ||
		target.Format != vault.FormatSnapshot || base.Format != vault.FormatSnapshot || base.CreatedAt.IsZero() ||
		base.CreatedAt.After(target.CreatedAt) || base.InstanceID != target.InstanceID || base.Name != target.Name || base.Profile != target.Profile ||
		target.Runtime == nil || target.Runtime.Installation == nil || base.Runtime == nil || base.Runtime.Installation == nil {
		return false
	}
	equal := func(a, b any) bool {
		x, e1 := json.Marshal(a)
		y, e2 := json.Marshal(b)
		return e1 == nil && e2 == nil && string(x) == string(y)
	}
	a, b := target.Runtime, base.Runtime
	return equal(target.ProfileDoc, base.ProfileDoc) && slices.Equal(target.LocalOverrides, base.LocalOverrides) &&
		reflect.DeepEqual(target.Roots, base.Roots) && a.Home == b.Home && a.OS == b.OS && a.Arch == b.Arch &&
		reflect.DeepEqual(a.Roots, b.Roots) && equal(a.Dependencies, b.Dependencies) &&
		a.Installation.InstallID == b.Installation.InstallID && a.Installation.DefaultInstanceID == b.Installation.DefaultInstanceID &&
		equal(a.Installation.ProfileDoc, b.Installation.ProfileDoc) && equal(a.Installation.Roots, b.Installation.Roots) &&
		equal(a.Installation.RegisteredProvenance, b.Installation.RegisteredProvenance) && equal(target.Provenance, base.Provenance)
}
