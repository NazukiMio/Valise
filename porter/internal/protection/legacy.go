package protection

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"valise/core/identity"
	cp "valise/core/protection"
	"valise/core/vaultrpc"
	"valise/porter/internal/seal"
)

func (s Store) legacy(a identity.AgentRef) (*cp.LegacySettings, error) {
	setting, err := seal.ReadPeriodSetting(seal.Paths{Home: s.Home}, a.InstanceID)
	if err != nil {
		return nil, err
	}
	v := &cp.LegacySettings{Scope: cp.Scope{Kind: "preset", Tier: "soul"}, Period: cp.Period{Unit: setting.Unit, Timezone: setting.TZ}, Copies: []cp.LegacyCopy{}}
	_, timer := names(a.InstanceID)
	unit, err := image(filepath.Join(s.Home, ".config", "systemd", "user", timer))
	if err != nil {
		return nil, err
	}
	v.TimerConfigured = unit.Exists
	// Only public schedule directives are projected; no ExecStart or environment.
	for _, line := range strings.Split(string(unit.Data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "OnUnitActiveSec":
			v.Every = value
		case "OnActiveSec":
			v.OnActive = value
		case "OnCalendar":
			v.Calendar = value
		}
	}
	b, err := os.ReadFile(filepath.Join(s.config(), "targets.json"))
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	var tf struct {
		Targets []vaultrpc.Target `json:"targets"`
	}
	if json.Unmarshal(b, &tf) != nil {
		return nil, fail("record_corrupt", "旧复制目标配置损坏")
	}
	for _, target := range tf.Targets {
		c := cp.LegacyCopy{TargetRef: cp.LocationRef{Kind: "legacy_target", ID: target.Name, AuthorityID: a.ServerUID}, Enabled: target.Auto != nil}
		if target.Auto != nil {
			c.Every = target.Auto.Every
		}
		v.Copies = append(v.Copies, c)
	}
	return v, nil
}
func (s Store) before(a identity.AgentRef) (cp.BeforeSettings, error) {
	p, err := s.load(a.InstanceID)
	if err != nil {
		return cp.BeforeSettings{}, err
	}
	if p != nil {
		return cp.BeforeSettings{Mode: "accepted", ServerPlan: p}, nil
	}
	legacy, err := s.legacy(a)
	return cp.BeforeSettings{Mode: "legacy", LegacySettings: legacy}, err
}
