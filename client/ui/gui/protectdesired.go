package gui

import (
	"strconv"
	"strings"
	"valise/core/protection"
)

// desired is the plan in force, or a starting point when there is none.
func (ps *protState) desired() protection.Desired {
	if ps.view != nil && ps.view.ServerPlan != nil {
		return sayCopies(ps.view.ServerPlan.Desired)
	}
	if ps.view != nil && ps.view.SupersededPlan != nil && ps.ref != nil {
		// The earlier generation's plan is the starting point; copies to
		// another server's targets are left for the person to add again.
		d := ps.view.SupersededPlan.Desired
		keep := []protection.ServerCopy{} // never nil: "no copies" must be said, not left out
		for _, cp := range d.ServerCopies {
			if cp.TargetRef.AuthorityID == ps.ref.ServerUID {
				keep = append(keep, cp)
			}
		}
		d.ServerCopies = keep
		return d
	}
	d := protection.NewDefault()
	d.Period = protection.Period{Unit: "month", Timezone: localZone()}
	return d
}

// sayCopies makes "no copies" an empty list. A plan must say its server
// copies in full; a missing list (null on the wire) is refused as an
// incomplete request (core/protection Validate), so nothing sends one.
func sayCopies(d protection.Desired) protection.Desired {
	if d.ServerCopies == nil {
		d.ServerCopies = []protection.ServerCopy{}
	}
	return d
}

func parseRetention(text string) (int, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, true
	}
	n, err := strconv.Atoi(text)
	return n, err == nil && n >= 0
}
