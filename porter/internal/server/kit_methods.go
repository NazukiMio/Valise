package server

import (
	"context"
	"encoding/json"
	"time"

	"valise/core/agentkit"
	"valise/core/valise"
	"valise/porter/internal/checkup"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/kit"
)

func init() {
	methods[agentkit.MethodCheckup] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[agentkit.CheckupParams](raw)
		if err != nil {
			return nil, err
		}
		rec, err := porterenroll.ReadRecord(s.cfg.Home, p.InstanceID)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, valise.New("instance_not_enrolled", "服务器上没有登记实例 "+p.InstanceID, "先运行 valise enroll <服务器> --accept")
		}
		prof, err := porterenroll.ReadProfile(s.cfg.Home, p.InstanceID)
		if err != nil {
			return nil, err
		}
		return checkup.Run(rec, prof, time.Now()), nil
	}
	methods[agentkit.MethodProposalsList] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[agentkit.ProposalsParams](raw)
		if err != nil {
			return nil, err
		}
		ps, err := kit.LoadProposals(s.cfg.Home, p.InstanceID)
		return agentkit.ProposalsResult{Proposals: ps}, err
	}
	methods[agentkit.MethodProposalsDecide] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[agentkit.DecideParams](raw)
		if err != nil {
			return nil, err
		}
		return kit.Decide(s.cfg.Home, p)
	}
	methods[agentkit.MethodKitInstall] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[agentkit.KitInstallParams](raw)
		if err != nil {
			return nil, err
		}
		return kit.Install(s.cfg.Home, p, time.Now())
	}
}
