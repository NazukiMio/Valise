package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/testbed/fakesshenv"
)

// A local fixture profile (harness names in tests are fixtures only).
const demoProfile = `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths:
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "demo.json", category: config, verify: pending }
  - { root: state, glob: "memory/**", category: memory }
provenance: { kind: binary, binary: { name: demo } }
`

func enrollWorld(t *testing.T) (*Service, string) {
	t.Helper()
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")
	for rel, body := range map[string]string{
		".demo/demo.json": "{}", ".demo/.env": "K=v", ".demo/memory/a.md": "m", ".demo/notes/x.md": "n",
		".local/bin/demo": "#!/bin/sh\necho demo 1.0\n",
	} {
		p := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o755)
	}
	s := newService(t, e.SSH)
	os.MkdirAll(filepath.Join(s.Dirs.Data, "profiles"), 0o755)
	os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(demoProfile), 0o644)
	return s, home
}

func TestEnrollReportThenAccept(t *testing.T) {
	s, home := enrollWorld(t)
	ctx := context.Background()
	res, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Report
	if !res.ProfileLocal || r.InstanceID != "demo" || len(r.Secrets) != 1 || r.Categories["memory"].Files != 1 ||
		r.Unclassified.Files != 1 || len(r.Pending) != 1 || r.Provenance.Binary == nil || res.Written != "" {
		t.Fatalf("%+v\n%+v", res, r)
	}
	record := filepath.Join(home, ".config/valise/dossiers/demo.json")
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("report-only enrollment wrote a record")
	}

	res, err = s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Assign: []string{"state:notes/**=memory"}, Accept: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Written != record || res.Report.Unclassified.Files != 0 || res.Report.Categories["memory"].Files != 2 {
		t.Fatalf("%+v %+v", res, res.Report)
	}
	// Re-enrollment sees the previous record and reports no changes.
	res, err = s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo"})
	if err != nil || res.Report.Previous == nil || len(res.Report.Changes) != 0 {
		t.Fatalf("%v %+v", err, res.Report)
	}
}

func TestEnrollErrors(t *testing.T) {
	s, _ := enrollWorld(t)
	ctx := context.Background()
	_, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "nope"})
	wantCode(t, err, "choose_instance")
	_, err = s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", InstanceID: "Bad"})
	wantCode(t, err, "instance_id_invalid")
	_, err = s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Assign: []string{"oops"}})
	wantCode(t, err, "assign_invalid")
	_, err = s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Assign: []string{"state:x=nonsense"}})
	wantCode(t, err, "override_invalid")
}

func TestDetectInstancesProfileVersions(t *testing.T) {
	s, _ := enrollWorld(t)
	ctx := context.Background()

	list, err := s.DetectInstances(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "demo" || list[0].Enrolled != "" || list[0].ProfileVersion != 1 || list[0].EnrolledProfileVersion != 0 {
		t.Fatalf("before enroll: %+v", list)
	}

	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	list, err = s.DetectInstances(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Enrolled != "demo" || list[0].ProfileVersion != 1 || list[0].EnrolledProfileVersion != 1 {
		t.Fatalf("after enroll v1: %+v", list)
	}

	v2 := strings.Replace(demoProfile, "profile_version: 1", "profile_version: 2", 1)
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err = s.DetectInstances(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Enrolled != "demo" || list[0].ProfileVersion != 2 || list[0].EnrolledProfileVersion != 1 {
		t.Fatalf("after local profile bump to v2: %+v", list)
	}
}
