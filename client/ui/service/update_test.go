package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"valise/testbed/fakesshenv"
)

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{
		{"0.0.0-dev", "v0.1.0", true}, {"v0.1.0", "v0.1.0", false}, {"0.1.0-dev", "0.1.0", true},
		{"v0.2.0", "v0.10.0", true}, {"v1.0.0", "v0.9.9", false}, {"garbage", "v0.1.0", true},
	} {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestCheckPorterUpdate(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	e.InstallPorter("box")
	s := newService(t, e.SSH)
	ctx := context.Background()
	u, err := s.CheckPorterUpdate(ctx, "box")
	if err != nil || !u.NoReleaseKey || u.Available {
		t.Fatalf("%+v %v", u, err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"tag_name":"profiles-2026.11.01"},{"tag_name":"v0.2.0","prerelease":true},{"tag_name":"v0.1.0"},{"tag_name":"v0.0.9"}]`))
	}))
	defer srv.Close()
	old := ReleasesAPI
	ReleasesAPI = srv.URL
	defer func() { ReleasesAPI = old }()
	s.Release.PublicKey = "configured"
	u, err = s.CheckPorterUpdate(ctx, "box")
	if err != nil || u.Latest != "v0.1.0" || !u.Available || u.Current == "" {
		t.Fatalf("%+v %v", u, err)
	}
}
