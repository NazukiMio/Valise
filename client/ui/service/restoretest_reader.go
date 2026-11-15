package service

import (
	"io"
	"valise/core/vault"
)

func (p *RevivePlan) writeRestoreFile(f vault.File, out io.Writer) error {
	return p.vault.WriteFile(f, out, p.ident)
}
