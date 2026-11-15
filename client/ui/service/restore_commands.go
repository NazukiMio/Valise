package service

import (
	"strings"
	"valise/client/ssh"
	"valise/core/revive"
)

// RestoreCommands uses the same argv builder as the fixed executor.
func RestoreCommands(p *RevivePlan) []string {
	var cmds [][]string
	if p.StopSource != "" {
		cmds = append(cmds, []string{"systemctl", "--user", "stop", p.StopSource})
	}
	if p.Plan.Unit != "" {
		cmds = append(cmds, []string{"systemctl", "--user", "stop", p.Plan.Unit}, []string{"systemctl", "--user", "start", p.Plan.Unit})
	}
	if p.Plan.Body.Action == revive.BodyNPMInstall {
		b := p.Plan.Body
		cmds = append(cmds, revive.NPMInstallArgv(b.Package, b.Version, b.Registry))
	}
	if len(p.InstallArgv) > 0 {
		cmds = append(cmds, p.InstallArgv)
	}
	var out []string
	for _, cmd := range cmds {
		var quoted []string
		for _, arg := range cmd {
			quoted = append(quoted, ssh.Quote(arg))
		}
		out = append(out, strings.Join(quoted, " "))
	}
	return out
}
