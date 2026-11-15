package service

import (
	"context"
	"path"
	"slices"
	"time"

	"valise/core/journey"
	"valise/core/stationsync"
	"valise/core/vaultrpc"
)

func journeyUnavailable(loc journey.LocationRef, code string) journey.Result {
	now := time.Now().UTC()
	return journey.Result{Partial: true, Observations: []journey.Observation{{LocationRef: loc, Status: "unknown", LastAttemptAt: now, Source: "remote", Error: code}}}
}

// The fixed-query adapter uses the same per-location readers as the stream.
func (s *Service) readRemoteTimeline(ctx context.Context) journey.Result {
	var result journey.Result
	reader := NewJourneyReader(nil)
	_ = reader.scanTimeline(ctx, nil, func(update TimelineUpdate) {
		result = journey.Result{Points: update.Page.Items, Observations: update.Page.Observations, Gaps: update.Page.Gaps, Partial: update.Page.Partial}
	}, s.addRemoteTimeline)
	return result
}

func (s *Service) addRemoteTimeline(scan *timelineScan) {
	servers, err := s.ServerList()
	if err != nil {
		loc := journey.LocationRef{Kind: "server_vault", ID: "servers", AuthorityID: "local"}
		scan.add(loc, func(context.Context) (journey.Result, error) { return journeyUnavailable(loc, "record_corrupt"), nil })
	}
	for _, host := range servers {
		loc := journey.LocationRef{Kind: "server_vault", ID: host.Name, AuthorityID: host.Name}
		if scan.wants(loc) {
			scan.add(loc, func(ctx context.Context) (journey.Result, error) {
				caps, err := s.BackupCapabilities(ctx, host.Name)
				if err != nil {
					return journey.Result{}, err
				}
				if !slices.Contains(caps, vaultrpc.JourneyCapability) {
					return journeyUnavailable(loc, "unsupported_capability"), nil
				}
				var res vaultrpc.JourneyResult
				if err := s.call(ctx, host.Name, vaultrpc.MethodJourney, vaultrpc.JourneyParams{}, &res); err != nil {
					return journey.Result{}, err
				}
				name := res.ServerName
				if name == "" {
					name = host.Name
				}
				for _, target := range res.Targets {
					if s.IsStation(target) {
						continue
					}
					tloc := journey.LocationRef{Kind: "legacy_target", ID: target, AuthorityID: host.Name}
					if scan.wants(tloc) {
						s.addTimelineTarget(scan, tloc, name)
					}
				}
				return remoteJourneyView(res.Result, loc, name), nil
			})
		}
		// Retrying a previously configured target still goes through its server's
		// fixed journey RPC; no arbitrary destination or file path is accepted.
		for _, wanted := range scan.only {
			if wanted.Kind == "legacy_target" && wanted.AuthorityID == host.Name {
				s.addTimelineTarget(scan, wanted, "")
			}
		}
	}
	for _, station := range s.Stations() {
		loc := journey.LocationRef{Kind: "station", ID: station.Name, AuthorityID: "local"}
		if !scan.wants(loc) {
			continue
		}
		scan.add(loc, func(ctx context.Context) (journey.Result, error) {
			st := s.relayStation(station)
			return s.localRemoteJourney(ctx, st, st.Dest(station.Path), loc)
		})
	}
	for _, cloud := range s.Config.Clouds {
		loc := journey.LocationRef{Kind: "cloud", ID: cloud.Name, AuthorityID: "local"}
		if !scan.wants(loc) {
			continue
		}
		scan.add(loc, func(ctx context.Context) (journey.Result, error) {
			st, err := s.hereStation(ctx, cloud.Name)
			if err != nil {
				return journey.Result{}, err
			}
			return s.localRemoteJourney(ctx, st, st.Dest(cloudFolder), loc)
		})
	}
}

func (s *Service) addTimelineTarget(scan *timelineScan, loc journey.LocationRef, serverName string) {
	scan.add(loc, func(ctx context.Context) (journey.Result, error) {
		var copy vaultrpc.JourneyResult
		if err := s.call(ctx, loc.AuthorityID, vaultrpc.MethodJourney, vaultrpc.JourneyParams{Target: loc.ID}, &copy); err != nil {
			return journey.Result{}, err
		}
		name := serverName
		if name == "" {
			name = copy.ServerName
		}
		if name == "" {
			name = loc.AuthorityID
		}
		return remoteJourneyView(copy.Result, loc, name), nil
	})
}

func remoteJourneyView(result journey.Result, loc journey.LocationRef, server string) journey.Result {
	if len(result.Observations) == 0 {
		now := time.Now().UTC()
		result.Observations = []journey.Observation{{LocationRef: loc, Status: "fresh", ObservedAt: now, LastAttemptAt: now, Source: "remote"}}
	}
	for i := range result.Observations {
		result.Observations[i].LocationRef, result.Observations[i].Source = loc, "remote"
	}
	for i := range result.Gaps {
		result.Gaps[i].LocationRef = loc
	}
	for i := range result.Points {
		for j := range result.Points[i].Copies {
			cp := &result.Points[i].Copies[j]
			cp.LocationRef, cp.Observation.LocationRef, cp.Observation.Source = loc, loc, "remote"
			if server != "" {
				leaf := cp.SourceLocator
				if leaf == "." {
					leaf = "-"
				}
				cp.SourceLocator = path.Join(server, leaf)
			}
		}
	}
	return result
}

func (s *Service) localRemoteJourney(ctx context.Context, st stationsync.Station, base string, loc journey.LocationRef) (journey.Result, error) {
	var result journey.Result
	_, _, err := stationsync.With(ctx, localRunner{}, st, func(r stationsync.Remote) error {
		var err error
		result, err = journey.ReadRemote(ctx, r, base, st.Tmp, loc)
		return err
	})
	return remoteJourneyView(result, loc, ""), err
}
