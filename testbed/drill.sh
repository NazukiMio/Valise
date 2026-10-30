#!/usr/bin/env bash
# Target-machine drill (see AGENT.md). Run on the developer Mac, which reaches
# the targets through the system OpenSSH and ~/.ssh/config:
#
#   testbed/drill.sh [host ...]        default: ubuntu-vm ubuntu-vm-x86 nas-test
#
# Each milestone appends its steps. Ends with ALL_PASS (failures=0) or exits 1.
set -uo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
hosts=("$@")
[ ${#hosts[@]} -gt 0 ] || hosts=(ubuntu-vm ubuntu-vm-x86 nas-test)

failures=0
pass() { printf 'PASS  %s\n' "$*"; }
fail() { printf 'FAIL  %s\n' "$*"; failures=$((failures + 1)); }

work="$(mktemp -d "${TMPDIR:-/tmp}/valise-drill.XXXXXX")"
echo "drill workdir: $work"

# ---------------------------------------------------------------- M1 -----
echo "== M1: build"
valise="$work/valise"
if (cd "$root/client/ui" && go build -o "$valise" ./cmd/valise); then
	pass "build valise CLI"
else
	fail "build valise CLI"; echo "ALL_FAIL (failures=$failures)"; exit 1
fi
for arch in amd64 arm64; do
	if (cd "$root/porter" && GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -o "$work/valise-porter_linux_$arch" .); then
		pass "build porter linux/$arch"
	else
		fail "build porter linux/$arch"
	fi
done

json_field() { # json_field <file> <python expression on d>
	python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$1" "$2"
}

for h in "${hosts[@]}"; do
	echo "== M1: $h"
	m="$(ssh -T -o BatchMode=yes -- "$h" 'uname -m' 2>/dev/null)"
	case "$m" in
		x86_64) arch=amd64 ;;
		aarch64 | arm64) arch=arm64 ;;
		*) fail "$h: uname -m = '$m'"; continue ;;
	esac
	pass "$h: reachable ($m)"

	if "$valise" porter deploy "$h" --local "$work/valise-porter_linux_$arch" > "$work/$h.deploy.txt" 2>&1; then
		pass "$h: porter deploy --local"
	else
		fail "$h: porter deploy --local"; cat "$work/$h.deploy.txt"; continue
	fi

	if ! "$valise" doctor "$h" --json > "$work/$h.doctor.json" 2> "$work/$h.doctor.err"; then
		fail "$h: valise doctor"; cat "$work/$h.doctor.err"; continue
	fi
	remote_home="$(ssh -T -o BatchMode=yes -- "$h" 'printf %s "$HOME"')"
	[ "$(json_field "$work/$h.doctor.json" 'd["porter_found"] and d.get("problem") is None')" = True ] \
		&& pass "$h: doctor finds porter, no problem" || fail "$h: doctor problem: $(cat "$work/$h.doctor.json")"
	[ "$(json_field "$work/$h.doctor.json" 'd["porter_path"]')" = "$remote_home/.local/bin/valise-porter" ] \
		&& pass "$h: porter in ~/.local/bin" || fail "$h: porter path $(json_field "$work/$h.doctor.json" 'd["porter_path"]')"
	[ "$(json_field "$work/$h.doctor.json" 'd["platform"]["os"]+"/"+d["platform"]["arch"]')" = "linux/$arch" ] \
		&& pass "$h: arch linux/$arch" || fail "$h: arch mismatch"
	[ "$(json_field "$work/$h.doctor.json" 'len(d["tools"])')" = 6 ] \
		&& pass "$h: tools reported" || fail "$h: tools missing"
	"$valise" doctor "$h"

	# The porter must be owned by the login user and the deploy must not
	# have needed sudo: nothing outside $HOME was touched.
	if ssh -T -o BatchMode=yes -- "$h" 'test -O "$HOME/.local/bin/valise-porter" && test -x "$HOME/.local/bin/valise-porter"'; then
		pass "$h: porter owned by login user (no sudo)"
	else
		fail "$h: porter ownership"
	fi
done

# ---------------------------------------------------------------- M2 -----
# Enrollment of the real harnesses (see AGENT.md target matrix).
#   host:profile:expected secrets (comma-separated, root-relative)
enrollments=(
	"ubuntu-vm:openclaw:openclaw.json"
	"ubuntu-vm:dsh:"
	"ubuntu-vm-x86:hermes:.env,auth.json,config.yaml"
)
in_hosts() { local h; for h in "${hosts[@]}"; do [ "$h" = "$1" ] && return 0; done; return 1; }

for e in "${enrollments[@]}"; do
	IFS=: read -r h p want <<<"$e"
	in_hosts "$h" || continue
	echo "== M2: $p on $h"
	out="$work/$h.$p.enroll.json"
	if ! "$valise" enroll "$h" --profile "$p" --report-json > "$out" 2> "$out.err"; then
		fail "$h/$p: enroll report"; cat "$out.err"; continue
	fi
	pass "$h/$p: enroll report"
	r() { json_field "$out" "$1"; }
	[ "$(r 'd["report"]["profile_id"]')" = "$p" ] && pass "$h/$p: profile matched" || fail "$h/$p: profile"
	for s in ${want//,/ }; do
		[ "$(r "any(x['path']=='$s' for x in d['report']['secrets'])")" = True ] \
			&& pass "$h/$p: secret $s identified" || fail "$h/$p: secret $s missing"
	done
	# Secrets are only ever declared files: nothing in the body is a secret.
	[ "$(r "all(not x['path'].startswith(('hermes-agent/','tools/','installs/')) for x in d['report']['secrets'])")" = True ] \
		&& pass "$h/$p: no secrets inside the body" || fail "$h/$p: secret inside body"
	kind="$(r 'd["report"]["provenance"]["kind"]')"
	case "$kind" in
		npm_global)
			[ -n "$(r 'd["report"]["provenance"]["npm"]["version"]')" ] && [ -n "$(r 'd["report"]["provenance"]["npm"].get("integrity","")')" ] \
				&& pass "$h/$p: npm provenance with integrity" || fail "$h/$p: npm provenance incomplete" ;;
		git)
			[ "$(r 'len(d["report"]["provenance"]["git"]["commit"])')" = 40 ] && [ -n "$(r 'd["report"]["provenance"]["git"]["remote"]')" ] \
				&& pass "$h/$p: git provenance" || fail "$h/$p: git provenance incomplete"
			[ "$(r 'd["report"]["categories"].get("body",{}).get("files",0) > 0')" = True ] \
				&& pass "$h/$p: body excluded from sealing" || fail "$h/$p: body not recognised" ;;
		*) fail "$h/$p: provenance kind '$kind'" ;;
	esac
	echo "   unclassified: $(r 'd["report"]["unclassified"]["files"]') files"

	if "$valise" enroll "$h" --profile "$p" --accept > "$work/$h.$p.accept.txt" 2>&1; then
		pass "$h/$p: enroll --accept"
	else
		fail "$h/$p: enroll --accept"; cat "$work/$h.$p.accept.txt"; continue
	fi
	if ssh -T -o BatchMode=yes -- "$h" "test \"\$(stat -c %a ~/.config/valise/instances/$p.json)\" = 600"; then
		pass "$h/$p: instance record written (0600)"
	else
		fail "$h/$p: instance record"
	fi
	"$valise" enroll "$h" --profile "$p" --report-json > "$out.2" 2>/dev/null
	[ "$(json_field "$out.2" 'd["report"]["previous"] is not None and not d["report"].get("changes")')" = True ] \
		&& pass "$h/$p: re-enroll sees record, no changes" || fail "$h/$p: re-enroll diff"
done

# ------------------------------------------------------------- result -----
if [ "$failures" -eq 0 ]; then
	echo "ALL_PASS (failures=0)"
else
	echo "FAILURES (failures=$failures)"
	exit 1
fi
