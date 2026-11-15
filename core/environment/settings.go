package environment

import (
	"valise/core/identity"
	"valise/core/protection"
)

const (
	SettingsCapability = "environment-settings/1"
	MethodSettingsGet  = "environment_settings_get"
	MethodSettingsSet  = "environment_settings_set"
)

type SettingsParams struct {
	Agent identity.AgentRef `json:"agent"`
}

type SetSettingsParams struct {
	Agent    identity.AgentRef `json:"agent"`
	Expected string            `json:"expected"`
	Mode     Mode              `json:"mode"`
}

// Settings describes policy, never proof of an active runtime or readiness.
type Settings struct {
	Agent    identity.AgentRef `json:"agent"`
	Mode     Mode              `json:"mode"`
	Managed  bool              `json:"managed"`
	Revision uint64            `json:"revision"`
	Busy     bool              `json:"busy"`
	Digest   string            `json:"digest"`
}

func (s Settings) Stamp() string {
	s.Digest = ""
	d, _ := protection.Digest(s)
	return d
}

func (s Settings) Validate() error {
	if protection.ValidateAgent(s.Agent) != nil || !validMode(s.Mode) || s.Managed && s.Revision == 0 || !s.Managed && s.Busy || s.Digest != s.Stamp() {
		return ErrState
	}
	return nil
}
