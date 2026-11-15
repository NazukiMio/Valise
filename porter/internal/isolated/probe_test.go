package isolated

import (
	"context"
	"io"
	"testing"
	"time"

	"valise/core/profile"
)

func TestProbeNeverRunsOnHost(t *testing.T) {
	if err := ProbeGuest(io.Discard); err != ErrUnsupported {
		t.Fatal("host accepted private namespace helper", err)
	}
	if stopPrivateChildren() {
		t.Fatal("host accepted private child cleanup")
	}
	if _, err := GatewayProbe(context.Background(), nil, Spec{}, ProbeCheck{}); err != ErrDeadline {
		t.Fatal("unbounded probe accepted", err)
	}
}

func TestGatewayNeedsExplicitHTTPProof(t *testing.T) {
	p := profile.Profile{Environments: &profile.Environments{GatewayEntry: "web"}, Entries: []profile.Entry{{ID: "web", Kind: "web", Host: "127.0.0.1", Port: 8080, Path: "/"}},
		Health: &profile.Health{Checks: []profile.HealthCheck{{Kind: "tcp", Entry: "web"}}}}
	if _, err := CheckFor(p); err == nil {
		t.Fatal("TCP treated as gateway readiness")
	}
	p.Health.Checks = append(p.Health.Checks, profile.HealthCheck{Kind: "http", Entry: "web", ExpectStatus: []int{200, 401}})
	if c, err := CheckFor(p); err != nil || !c.valid() {
		t.Fatal(c, err)
	}
	for _, bad := range []ProbeCheck{
		{Host: "10.0.0.1", Port: 80, Path: "/", Statuses: []int{200}, Timeout: time.Second},
		{Host: "127.0.0.1", Port: 80, Path: "/?token=secret", Statuses: []int{200}, Timeout: time.Second},
		{Host: "127.0.0.1", Port: 80, Path: "/", Statuses: []int{200}, Timeout: time.Hour},
	} {
		if bad.valid() {
			t.Fatal("unsupported probe accepted")
		}
	}
	if (ProbeResult{GatewayPassed: true}).Verified() || (ProbeResult{Completed: true, GatewayPassed: true, ProcessStarted: true, PortListening: true, HTTPResponded: true}).Verified() {
		t.Fatal("incomplete process cleanup treated as verified")
	}
}
