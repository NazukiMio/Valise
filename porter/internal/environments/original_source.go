package environments

import "context"

// VerifyOriginalSource checks the still-native source against its independently
// copied rollback material. It reads no programme from an implicit PATH and
// never treats the rollback copy alone as proof of the running source.
func VerifyOriginalSource(ctx context.Context, b *ProgramBinding, expected string) error {
	if b == nil || b.Runtime == nil || b.Runtime.Installation == nil || b.Origin != "installed_original" || Digest(b) != expected {
		return ErrSource
	}
	in := InstalledSource{Profile: b.Profile, Overrides: b.Overrides, Layout: b.Layout, Origin: b.Runtime.Installation.RegisteredProvenance, MaxBytes: 8 << 30}
	observed, err := ObserveInstalledNPM(ctx, in)
	if err != nil || Digest(observed.Dependencies) != Digest(b.Runtime.Dependencies) || Digest(observed.Material.Files) != Digest(b.Program.Files) || observed.Material.SourceDigest != b.Program.SourceDigest {
		return ErrSource
	}
	return nil
}
