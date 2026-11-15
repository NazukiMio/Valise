package service

import "valise/core/journey"

// Called under the query reader's mutex. A failed refresh keeps previously
// observed metadata explicitly stale; it never creates current copy evidence.
func (r *JourneyReader) retainRemote(current journey.Result) journey.Result {
	if r.remoteCache == nil {
		r.remoteCache = map[journey.LocationRef]journey.Result{}
	}
	for i, obs := range current.Observations {
		if obs.Status == "fresh" {
			cached := journey.Result{Observations: []journey.Observation{obs}}
			for _, gap := range current.Gaps {
				if gap.LocationRef == obs.LocationRef {
					cached.Gaps = append(cached.Gaps, gap)
					cached.Partial = true
				}
			}
			for _, p := range current.Points {
				copies := []journey.Copy{}
				for _, cp := range p.Copies {
					if cp.LocationRef == obs.LocationRef {
						copies = append(copies, cp)
					}
				}
				if len(copies) > 0 {
					p.Copies = copies
					cached.Points = append(cached.Points, p)
				}
			}
			r.remoteCache[obs.LocationRef] = cached
			continue
		}
		cached, ok := r.remoteCache[obs.LocationRef]
		if !ok {
			continue
		}
		stale := cached.Observations[0]
		stale.Status, stale.LastAttemptAt, stale.Error = "stale", obs.LastAttemptAt, obs.Error
		current.Observations[i] = stale
		for _, p := range cached.Points {
			p.Copies = append([]journey.Copy(nil), p.Copies...)
			for j := range p.Copies {
				p.Copies[j].Observation = stale
			}
			current.Points = append(current.Points, p)
		}
		current.Partial = true
	}
	return journey.Merge(current)
}
