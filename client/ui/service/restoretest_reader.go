package service

import (
	"io"
	"valise/core/vault"
)

// The private map comes only from the verified composition plan, never from a
// request path. Ordinary restore and file-only tests use their original vault.
func (p *RevivePlan) writeRestoreFile(f vault.File, out io.Writer) error {
	if source := p.testDataSources[f.Root+"\x00"+f.Path]; source != nil {
		return source.vault.WriteFile(f, out, source.ident)
	}
	return p.vault.WriteFile(f, out, p.ident)
}
