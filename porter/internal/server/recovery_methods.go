package server

import (
	"context"
	"encoding/json"
	"os/user"

	core "valise/core/recovery"
	"valise/porter/internal/protection"
	"valise/porter/internal/recovery"
)

// Finding the Valise passphrase again (docs/ARCHITECTURE.md 3.2): the
// daily key places a request in the user's own folder and fetches the
// answer the user's sudo command wrote.
func init() {
	methods[core.MethodRequest] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[core.RequestParams](raw)
		if err != nil {
			return nil, err
		}
		if err := recovery.Place(s.cfg.Home, p); err != nil {
			return nil, err
		}
		res := core.RequestResult{Porter: protection.DeployedPorter(s.cfg.Home)}
		if u, err := user.Current(); err == nil {
			res.User = u.Username
		}
		return res, nil
	}
	methods[core.MethodResult] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[core.IDParams](raw)
		if err != nil {
			return nil, err
		}
		ct, err := recovery.Result(s.cfg.Home, p.ID)
		return core.ResultResult{Ciphertext: ct}, err
	}
	methods[core.MethodClear] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[core.IDParams](raw)
		if err != nil {
			return nil, err
		}
		return struct{}{}, recovery.Clear(s.cfg.Home, p.ID)
	}
}
