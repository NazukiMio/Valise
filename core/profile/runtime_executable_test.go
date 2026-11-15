package profile

import (
	"strings"
	"testing"
)

func TestRuntimeExecutableMustBeExplicitlyBoundToDeclaredUserService(t *testing.T) {
	good := strings.Replace(environmentYAML, `start_argv: ["${HOME}/program/bin/example", serve]`, `start_argv: ["${RUNTIME_EXECUTABLE}", serve]`, 1)
	good = strings.Replace(good, `path: ["${HOME}/program/bin"]`, `path: ["${RUNTIME_DIR}"]`, 1)
	good = strings.Replace(good, "engine: linux-bwrap", "engine: linux-bwrap\n  runtime_executable: {service: {scope: user, unit: example.service}, name: engine}", 1)
	d := mustParse(t, "fixture", baseYAML+good)
	if issues := Lint([]*Document{d})[0].Issues; len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, tc := range []struct{ old, replacement string }{
		{"unit: example.service", "unit: unrelated.service"},
		{"scope: user, unit: example.service", "scope: system, unit: example.service"},
		{"name: engine", "name: ../engine"},
		{`["${RUNTIME_EXECUTABLE}", serve]`, `["${HOME}/shared", serve]`},
		{"  runtime_executable: {service: {scope: user, unit: example.service}, name: engine}\n", ""},
		{`path: ["${RUNTIME_DIR}"]`, `path: ["${env:PATH}"]`},
	} {
		if issues := lintOne(t, baseYAML+strings.Replace(good, tc.old, tc.replacement, 1)); len(issues) == 0 {
			t.Fatal("unbound runtime declaration accepted", tc.replacement)
		}
	}
}
