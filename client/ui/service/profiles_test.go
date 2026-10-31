package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"valise/client/minisign/minisigntest"
	"valise/client/profiles"
)

func TestFetchPack(t *testing.T) {
	signer := minisigntest.New()
	tgz, err := profiles.Build("../../../profiles", "2026.10.31", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sig := signer.Sign(tgz, "file:"+profiles.PackAsset)
	mux := http.NewServeMux()
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"tag_name":"v0.1.0"},{"tag_name":"profiles-2026.10.31"},{"tag_name":"profiles-2026.10.20"},{"tag_name":"profiles-2027.01.01","draft":true}]`))
	})
	mux.HandleFunc("/dl/profiles-2026.10.31/"+profiles.PackAsset, func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) })
	mux.HandleFunc("/dl/profiles-2026.10.31/"+profiles.PackSigAsset, func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	old := ReleasesAPI
	ReleasesAPI = srv.URL + "/releases"
	defer func() { ReleasesAPI = old }()

	s := newService(t, "ssh")
	s.Release.BaseURL = srv.URL + "/dl"
	s.Release.PublicKey = signer.PublicKey
	if _, err := s.Enroll(context.Background(), "box", EnrollOptions{}); err == nil {
		t.Fatal("enroll without profiles")
	} else {
		wantCode(t, err, "no_profiles")
	}
	f, err := s.FetchPack(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if f.From != "profiles-2026.10.31" || f.SignatureErr != nil {
		t.Fatalf("%s %v", f.From, f.SignatureErr)
	}
	if err := s.InstallPack(f); err != nil {
		t.Fatal(err)
	}
	st, err := s.ProfilesStatus()
	if err != nil || st.Pack == nil || len(st.Local) != 0 {
		t.Fatalf("%+v %v", st, err)
	}
	// Without the key the same download is only installable as local.
	s.Release.PublicKey = ""
	f, err = s.FetchPack(context.Background(), "profiles-2026.10.31")
	if err != nil || f.SignatureErr == nil {
		t.Fatal(err)
	}
	if err := s.InstallPack(f); err == nil {
		t.Fatal("installed an unverifiable pack")
	}
}
