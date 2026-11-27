package profile

import "testing"

func TestEntryStartCaptureURLLint(t *testing.T) {
	yamlDoc := `schema: valise-profile/1
id: demo-capture
display_name: Demo Capture
profile_version: 1
status: draft
detect:
  state_dir_candidates: ["${HOME}/.demo"]
  markers_any: [config.yaml]
provenance:
  kind: binary
  binary: { name: demo }
entries:
  - id: webapp
    display_name: WebApp
    kind: web
    host: 127.0.0.1
    port: 9120
    path: "/"
    start:
      summary: "start webapp"
      argv: [demo, webapp]
      wait_seconds: 25
      capture_url: true
      verify: pending
`
	doc, err := Parse([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	doc.Path = "demo-capture.yaml"
	if len(doc.Profile.Entries) != 1 || doc.Profile.Entries[0].Start == nil || !doc.Profile.Entries[0].Start.CaptureURL {
		t.Fatalf("expected CaptureURL=true, got %+v", doc.Profile.Entries)
	}
	res := Lint([]*Document{doc})
	if len(res) != 1 || len(res[0].Issues) != 0 {
		t.Fatalf("unexpected lint issues: %+v", res)
	}
}
