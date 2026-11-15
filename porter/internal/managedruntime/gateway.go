package managedruntime

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"valise/core/profile"
	"valise/porter/internal/isolated"
)

type GatewayObservation struct {
	UnitRunning   bool `json:"unit_running"`
	PortOwned     bool `json:"port_owned"`
	HTTPResponded bool `json:"http_responded"`
	HTTPStatus    int  `json:"http_status,omitempty"`
	GatewayPassed bool `json:"gateway_passed"`
}

// CheckHTTP verifies the declared loopback listener belongs to this unit,
// rather than mistaking another server on the host for the restored gateway.
// Only fixed profile status codes are checked; response bodies are not read.
func (c Controller) CheckHTTP(ctx context.Context, d Definition, p profile.Profile) (GatewayObservation, error) {
	return checkOwnedHTTP(ctx, d.name, p, func(ctx context.Context) (Observation, error) { return c.Read(ctx, d) })
}

func (c Controller) CheckNativeHTTP(ctx context.Context, ref NativeRef, p profile.Profile) (GatewayObservation, error) {
	if err := ref.Validate(p); err != nil {
		return GatewayObservation{}, err
	}
	return checkOwnedHTTP(ctx, ref.Unit, p, func(ctx context.Context) (Observation, error) {
		ob, err := c.ReadNative(ctx, ref)
		if err == nil && !sameNativeProcess(ref, ob) {
			err = ErrState
		}
		return ob, err
	})
}

func checkOwnedHTTP(ctx context.Context, unit string, p profile.Profile, read func(context.Context) (Observation, error)) (GatewayObservation, error) {
	var result GatewayObservation
	if ctx == nil {
		return result, ErrState
	}
	check, err := isolated.CheckFor(p)
	if err != nil {
		return result, ErrState
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	before, err := read(ctx)
	if err != nil || !before.Running || before.PendingJob {
		return result, ErrState
	}
	result.UnitRunning = true
	if !ownedListener(before.ControlGroup, unit, check.Host, check.Port) {
		return result, ErrState
	}
	result.PortOwned = true
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 8 << 10, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	uri := url.URL{Scheme: "http", Host: net.JoinHostPort(check.Host, strconv.Itoa(check.Port)), Path: check.Path}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, uri.String(), nil)
	if err != nil {
		return result, ErrState
	}
	response, err := client.Do(request)
	if err != nil {
		return result, ErrState
	}
	result.HTTPResponded, result.HTTPStatus = true, response.StatusCode
	response.Body.Close()
	after, err := read(ctx)
	if err != nil || !after.Running || after.PendingJob || before.MainPID != after.MainPID || before.KernelStart != after.KernelStart || !ownedListener(after.ControlGroup, unit, check.Host, check.Port) {
		return result, ErrState
	}
	for _, status := range check.Statuses {
		if status == result.HTTPStatus {
			result.GatewayPassed = true
			return result, nil
		}
	}
	return result, ErrState
}
