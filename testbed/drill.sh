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

# ---------------------------------------------------------------- M3 -----
# Sealing, vault, sync, pull and restore without Valise. A dedicated test
# identity lives in $VALISE_DRILL_KEYDIR (never the user's real keystore) and
# is reused across runs so servers do not collect test recipients.
keydir="${VALISE_DRILL_KEYDIR:-$HOME/.cache/valise-drill}"
mkdir -p "$keydir" && chmod 700 "$keydir"
if [ ! -f "$keydir/config/keystore.age" ]; then
	age-keygen -o "$keydir/identity.txt" 2>/dev/null
	LC_ALL=C tr -dc 'a-z0-9' </dev/urandom | head -c 24 > "$keydir/passphrase.txt"
	chmod 600 "$keydir/identity.txt" "$keydir/passphrase.txt"
	"$valise" --config-dir "$keydir/config" key import --file "$keydir/identity.txt" < "$keydir/passphrase.txt" >/dev/null
fi
V() { "$valise" --config-dir "$keydir/config" "$@"; }
restore_py="$root/tools/standalone-restore/restore.py"
nas_dest="nas-test:/mnt/test/nas/valise-vault/"

seal_json() { # seal_json <host> <instance> <tier> -> prints snapshot_id added_bytes
	V seal "$1" "$2" --tier "$3" --json 2>"$work/seal.err" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["snapshot_id"], d["added_bytes"])'
}

m3_hosts=()
for e in "${enrollments[@]}"; do
	IFS=: read -r h p want <<<"$e"
	in_hosts "$h" || continue
	case " ${m3_hosts[*]} " in *" $h "*) ;; *)
		m3_hosts+=("$h")
		echo "== M3: keys and target on $h"
		V key register "$h" --label "valise-drill TEST key (identity in ~/.cache/valise-drill on the dev Mac); safe to remove once valise key unregister exists" >/dev/null && pass "$h: key registered" || fail "$h: key register"
		V target add "$h" nas --rsync "$nas_dest" >/dev/null && pass "$h: sync target" || fail "$h: target add"
	;; esac
	echo "== M3: seal $p on $h"
	read -r s1 a1 < <(seal_json "$h" "$p" soul) || { fail "$h/$p: seal soul"; cat "$work/seal.err"; continue; }
	read -r s2 a2 < <(seal_json "$h" "$p" soul)
	read -r ss as < <(seal_json "$h" "$p" soul-secrets)
	[ -n "$ss" ] && pass "$h/$p: sealed soul, soul and soul-secrets" || fail "$h/$p: seal"
	# "First" is the instance's earliest soul seal in this vault.
	first="$(V vault status "$h" --json | python3 -c 'import json,sys; s=[m for m in json.load(sys.stdin)["snapshots"] if m["instance_id"]==sys.argv[1] and m["tier"]=="soul"]; print(min(s,key=lambda m:m["snapshot_id"])["added_bytes"])' "$p")"
	echo "   unchanged re-seal added $a2 bytes; first soul seal of $p added $first ($(python3 -c "print(round(100*$a2/$first,2))")%)"
	if [ "$first" -ge 1048576 ]; then # meaningful once the instance holds real data (fixed overhead ~1.3 KiB)
		[ $((a2 * 100)) -lt "$first" ] && pass "$h/$p: re-seal < 1% of the first seal" || fail "$h/$p: re-seal not deduplicated"
	fi
	echo "$h $p $s1 $ss $want" >> "$work/m3-snaps.txt"
done

for h in "${m3_hosts[@]}"; do
	echo "== M3: vault on $h"
	V vault verify "$h" >/dev/null && pass "$h: server vault verifies (no key)" || fail "$h: server vault verify"
	V sync "$h" nas >/dev/null && pass "$h: synced to nas" || fail "$h: sync"
	vid="$(V vault status "$h" --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["vault_id"])')"
	V pull "$h" --to "$work/pull-$h" >/dev/null && pass "$h: pulled" || fail "$h: pull"
	V verify --vault "$work/pull-$h" --key < "$keydir/passphrase.txt" >/dev/null \
		&& pass "$h: local copy verifies with the key" || fail "$h: verify --key"
	cp -R "$work/pull-$h" "$work/tamper-$h"
	python3 -c 'import sys,glob; p=sorted(glob.glob(sys.argv[1]+"/packs/*/*.pack"))[0]; b=bytearray(open(p,"rb").read()); b[len(b)//2]^=1; open(p,"wb").write(b)' "$work/tamper-$h"
	if V verify --vault "$work/tamper-$h" >/dev/null 2>&1; then fail "$h: tampered pack not detected"; else pass "$h: tampered pack detected"; fi

	# Restore from the NAS copy with restore.py + age + zstd only.
	nas="$work/nas-$h"
	rsync -a "nas-test:/mnt/test/nas/valise-vault/$vid/" "$nas/" && pass "$h: fetched NAS copy" || { fail "$h: fetch NAS copy"; continue; }
	while read -r hh p s1 ss want; do
		[ "$hh" = "$h" ] || continue
		python3 -I "$restore_py" --vault "$nas" --identity "$keydir/identity.txt" --snapshot "$s1" --out "$work/r-$p-soul" --include-secrets >/dev/null \
			&& pass "$h/$p: restore.py soul" || { fail "$h/$p: restore.py soul"; continue; }
		python3 -I "$restore_py" --vault "$nas" --identity "$keydir/identity.txt" --snapshot "$ss" --out "$work/r-$p-ss" --include-secrets >/dev/null \
			&& pass "$h/$p: restore.py soul-secrets" || { fail "$h/$p: restore.py soul-secrets"; continue; }
		for s in ${want//,/ }; do
			[ ! -e "$work/r-$p-soul/state/$s" ] && pass "$h/$p: soul tier has no $s" || fail "$h/$p: soul tier contains $s"
			[ -e "$work/r-$p-ss/state/$s" ] && pass "$h/$p: soul-secrets has $s" || fail "$h/$p: soul-secrets lacks $s"
		done
		# Byte-for-byte against the live files; SQLite copies and files the
		# running harness rewrote after the seal are reported, not failed.
		state_dir="$(json_field "$work/$h.$p.enroll.json" '[r["path"] for r in d["report"]["roots"] if r["id"]=="state"][0]')"
		(cd "$work/r-$p-ss/state" && find . -type f | sed 's|^\./||' | sort) > "$work/$p.list"
		(cd "$work/r-$p-ss/state" && while IFS= read -r f; do shasum -a 256 "$f"; done < "$work/$p.list") | sort -k2 > "$work/$p.local"
		ssh -T -o BatchMode=yes -- "$h" "cd '$state_dir' && while IFS= read -r f; do sha256sum \"\$f\"; done" < "$work/$p.list" | sort -k2 > "$work/$p.remote"
		diffs="$(diff "$work/$p.local" "$work/$p.remote" | grep '^<' | awk '{print $3}')"
		dbs="$(json_field "$work/$h.$p.enroll.json" '" ".join(x["path"] for x in d["report"]["sqlite"])')"
		sealed_at="$(python3 -c 'import json,sys; print(int(__import__("datetime").datetime.fromisoformat(json.load(open(sys.argv[1]))["created_at"][:19]+"+00:00").timestamp()))' "$nas/snapshots/$ss.meta.json")"
		bad=0
		for f in $diffs; do
			case " $dbs " in *" $f "*) continue ;; esac
			mt="$(ssh -T -o BatchMode=yes -- "$h" "stat -c %Y '$state_dir/$f'")"
			if [ "$mt" -ge "$sealed_at" ]; then
				echo "   changed after the seal (live harness): $f"
			else
				echo "   MISMATCH (unchanged since the seal): $f"; bad=1
			fi
		done
		[ "$bad" = 0 ] && pass "$h/$p: $(wc -l < "$work/$p.list" | tr -d ' ') restored files byte-identical to the live instance" \
			|| fail "$h/$p: restored files differ from the live instance"
	done < "$work/m3-snaps.txt"
done

# ------------------------------------------------------------- result -----
if [ "$failures" -eq 0 ]; then
	echo "ALL_PASS (failures=0)"
else
	echo "FAILURES (failures=$failures)"
	exit 1
fi
