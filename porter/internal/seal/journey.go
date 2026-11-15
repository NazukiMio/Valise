package seal

import (
	"context"
	"os"

	"valise/core/journey"
	"valise/core/stationsync"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// Journey lists only the configured vault or one configured copy target.
func Journey(ctx context.Context, env *porterenroll.Env, p Paths, target string) (*vaultrpc.JourneyResult, error) {
	name, err := ReadServerName(p)
	if err != nil {
		return nil, err
	}
	out := &vaultrpc.JourneyResult{ServerName: name}
	loc := journey.LocationRef{Kind: "server_vault", ID: "server", AuthorityID: "server"}
	if target != "" {
		t, err := findTarget(p, target)
		if err != nil {
			return nil, err
		}
		loc.Kind, loc.ID = "legacy_target", target
		_, _, err = withRemote(ctx, env, p, t, func(r stationsync.Remote) error {
			var err error
			out.Result, err = journey.ReadRemote(ctx, r, treeDest(t, name), p.Tmp(), loc)
			return err
		})
		return out, err
	}
	var sources []journey.Source
	for _, dir := range []string{p.Vaults(), p.LegacyVault()} {
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			sources = append(sources, journey.Source{LocationRef: loc, Root: dir})
		}
	}
	out.Result, err = journey.Scan(ctx, sources)
	if err != nil {
		return nil, err
	}
	targets, err := readTargets(p)
	if err != nil {
		out.Result.Partial = true
		out.Result.Gaps = append(out.Result.Gaps, journey.Gap{LocationRef: loc, Code: "targets_unreadable"})
		return out, nil
	}
	for _, t := range targets {
		out.Targets = append(out.Targets, t.Name)
	}
	return out, err
}
