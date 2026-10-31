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
	V seal "$h" "$p" --tier soul-secrets --json > "$work/$p.ss.json" 2>"$work/seal.err"
	ss="$(json_field "$work/$p.ss.json" 'd["snapshot_id"]')"
	[ -n "$ss" ] && pass "$h/$p: sealed soul, soul and soul-secrets" || fail "$h/$p: seal"
	# Unknown means sensitive: every unclassified file is in soul-secrets.
	want_unc="$(json_field "$work/$h.$p.enroll.json" 'd["report"]["unclassified"]["files"]')"
	[ "$(json_field "$work/$p.ss.json" 'd["categories"].get("unclassified",{}).get("files",0)')" = "$want_unc" ] \
		&& pass "$h/$p: $want_unc unclassified files sealed with the secrets layer" || fail "$h/$p: unclassified files missing from soul-secrets"
	if [ "${VALISE_DRILL_RAW:-0}" = 1 ]; then # large: the whole instance, body included
		read -r sr ar < <(seal_json "$h" "$p" raw) && pass "$h/$p: sealed raw ($ar bytes added)" || fail "$h/$p: seal raw"
		read -r sr2 ar2 < <(seal_json "$h" "$p" raw)
		echo "   unchanged raw re-seal added $ar2 bytes"
	fi
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

# ---------------------------------------------------------------- M4 -----
# Revive. Stops and restarts harness services on the targets, so it runs only
# with VALISE_DRILL_REVIVE=1.
#   VALISE_DRILL_REVIVE_ON   host:instance for rollback, fault and crash
#   VALISE_DRILL_MIGRATE     source:target:instance for clone, then migrate
# Hermes on ubuntu-vm-x86 is not the default: its dashboard runs outside the
# gateway unit and blocks a swap (by design; Valise never kills processes).
if [ "${VALISE_DRILL_REVIVE:-0}" = 1 ]; then
	IFS=: read -r rh ri <<<"${VALISE_DRILL_REVIVE_ON:-ubuntu-vm:openclaw}"
	IFS=: read -r ms mt mi <<<"${VALISE_DRILL_MIGRATE:-ubuntu-vm:ubuntu-vm-x86:openclaw}"
	R() { # R <args...>: revive with the drill passphrase; output in $work/revive.out
		V revive "$@" --allow-draft-profile --yes < "$keydir/passphrase.txt" > "$work/revive.out" 2>&1
	}
	opid() { sed -n 's/^复活计划.*op \(op_[0-9A-Z]*\).*/\1/p' "$work/revive.out" | head -1; }
	unit_active() { ssh -T -o BatchMode=yes -- "$1" "systemctl --user is-active '$2'" 2>/dev/null | grep -qx active; }
	# State root of an instance, and a listing of every file with its digest
	# and mtime; the root's inode tells whether the original directory is back.
	state_root() { ssh -T -o BatchMode=yes -- "$1" "python3 -c 'import json,os; print(json.load(open(os.path.expanduser(\"~/.config/valise/instances/$2.json\")))[\"roots\"][\"state\"])'"; }
	snap() { # snap <host> <dir> <out>
		ssh -T -o BatchMode=yes -- "$1" "stat -c %i '$2'; cd '$2' && find . -type f -printf '%P\t%T@\n' | sort | while IFS=\$(printf '\t') read -r f m; do printf '%s\t%s\t%s\n' \"\$(sha256sum < \"\$f\" | cut -c1-64)\" \"\${m%.*}\" \"\$f\"; done" > "$3"
	}
	# baseline <host> <unit> <dir> <out>: the listing taken while the service
	# is stopped. A running harness removes its own locks and temp files when
	# it stops (OpenClaw: tmp/openclaw-<uid>/*.lock, tmp/plugin-captures/<id>/),
	# so a listing of the running instance cannot be compared after a swap.
	baseline() {
		ssh -T -o BatchMode=yes -- "$1" "systemctl --user stop '$2'"
		snap "$1" "$3" "$4"
		t0="$(date +%s)"
		ssh -T -o BatchMode=yes -- "$1" "systemctl --user start '$2'; for i in 1 2 3 4 5 6 7 8 9 10; do systemctl --user is-active -q '$2' && break; sleep 1; done; sleep 10"
	}
	# same_state <before> <after> <since>: same directory, and every file that
	# differs was written by the running harness after <since>.
	same_state() {
		python3 - "$1" "$2" "$3" <<'EOF'
import sys
def load(p):
    lines = open(p).read().splitlines()
    return lines[0], {l.split("\t")[2]: (l.split("\t")[0], int(l.split("\t")[1])) for l in lines[1:]}
ia, a = load(sys.argv[1]); ib, b = load(sys.argv[2]); since = int(sys.argv[3])
# Harness scratch space (a "tmp" path segment) is created and swept by the
# harness itself across restarts; it is counted, not compared.
tmp = lambda f: f.startswith("tmp/") or "/tmp/" in f
bad = [f for f in a if f in b and a[f][0] != b[f][0] and b[f][1] < since and not tmp(f)]
gone = [f for f in a if f not in b and not tmp(f)]
swept = [f for f in a if f not in b and tmp(f)]
for f in bad: print("   MISMATCH (not rewritten since):", f)
for f in gone: print("   MISSING:", f)
if swept: print("   %d harness temp files swept by the harness (not compared)" % len(swept))
sys.exit(0 if ia == ib and not bad and not gone else 1)
EOF
	}
	unit="$(V doctor "$rh" >/dev/null 2>&1; ssh -T -o BatchMode=yes -- "$rh" "python3 -c 'import json,os; print(json.load(open(os.path.expanduser(\"~/.config/valise/instances/$ri.json\")))[\"service\"][0][\"unit\"])'")"
	dir="$(state_root "$rh" "$ri")"

	echo "== M4: rollback $ri on $rh"
	read -r soul _ < <(seal_json "$rh" "$ri" soul)
	if R "$rh" --snapshot "$soul" --mode rollback && grep -q '复活完成，服务健康' "$work/revive.out"; then
		pass "$rh/$ri: rollback to a soul snapshot is healthy"
		V prev drop "$rh" "$(opid)" --yes >/dev/null && pass "$rh/$ri: prev dropped" || fail "$rh/$ri: prev drop"
	else
		fail "$rh/$ri: rollback"; cat "$work/revive.out"
	fi

	echo "== M4: fault injection (health) on $rh"
	read -r soul _ < <(seal_json "$rh" "$ri" soul)
	baseline "$rh" "$unit" "$dir" "$work/before.txt"
	R "$rh" --snapshot "$soul" --mode rollback --inject-fault health
	if grep -q '结果：rolled_back' "$work/revive.out"; then pass "$rh/$ri: failed health check rolled back"; else fail "$rh/$ri: no rollback on failed health"; cat "$work/revive.out"; fi
	snap "$rh" "$dir" "$work/after.txt"
	same_state "$work/before.txt" "$work/after.txt" "$t0" && pass "$rh/$ri: original state directory back, files unchanged" || fail "$rh/$ri: state changed by a rolled-back revive"
	unit_active "$rh" "$unit" && pass "$rh/$ri: $unit active again" || fail "$rh/$ri: $unit not active"
	V prev drop "$rh" "$(opid)" --yes >/dev/null

	echo "== M4: crash at LIVE_MOVED on $rh"
	baseline "$rh" "$unit" "$dir" "$work/before.txt"
	R "$rh" --snapshot "$soul" --mode rollback --inject-fault crash-at=LIVE_MOVED
	op="$(opid)"
	pending="$(V doctor "$rh" --json | python3 -c 'import json,sys; print(" ".join(p["op_id"]+":"+p["state"] for p in json.load(sys.stdin).get("pending_ops") or []))')"
	[ "$pending" = "$op:LIVE_MOVED" ] && pass "$rh: doctor reports the unfinished op" || fail "$rh: pending ops = '$pending', want $op:LIVE_MOVED"
	V revive resume "$rh" "$op" --back > "$work/resume.out" 2>&1 && grep -q 'rolled_back' "$work/resume.out" \
		&& pass "$rh/$ri: resume --back rolled back" || { fail "$rh/$ri: resume --back"; cat "$work/resume.out"; }
	snap "$rh" "$dir" "$work/after.txt"
	same_state "$work/before.txt" "$work/after.txt" "$t0" && pass "$rh/$ri: original state directory back after the crash" || fail "$rh/$ri: state changed after crash + back"
	unit_active "$rh" "$unit" && pass "$rh/$ri: $unit active again" || fail "$rh/$ri: $unit not active"
	V prev drop "$rh" "$op" --yes >/dev/null

	if in_hosts "$ms" && in_hosts "$mt"; then
		echo "== M4: clone $mi $ms -> $mt"
		munit="$(ssh -T -o BatchMode=yes -- "$ms" "python3 -c 'import json,os; print(json.load(open(os.path.expanduser(\"~/.config/valise/instances/$mi.json\")))[\"service\"][0][\"unit\"])'")"
		unit_active "$ms" "$munit" || ssh -T -o BatchMode=yes -- "$ms" "systemctl --user start '$munit'"
		read -r full _ < <(seal_json "$ms" "$mi" full)
		if R "$mt" --from "$ms" --snapshot "$full" --mode clone && grep -q '复活完成，服务健康' "$work/revive.out"; then
			pass "$ms->$mt: clone healthy"
			unit_active "$ms" "$munit" && pass "$ms: source still running next to the clone" || fail "$ms: source stopped by a clone"
			V prev drop "$mt" "$(opid)" --yes >/dev/null 2>&1
		else
			fail "$ms->$mt: clone"; cat "$work/revive.out"
		fi
		echo "== M4: migrate $mi $ms -> $mt (over the clone)"
		read -r full _ < <(seal_json "$ms" "$mi" full)
		if R "$mt" --from "$ms" --snapshot "$full" --mode migrate && grep -q '复活完成，服务健康' "$work/revive.out"; then
			pass "$ms->$mt: migrate healthy"
			unit_active "$ms" "$munit" && fail "$ms: source still running after migrate" || pass "$ms: source service stopped (files untouched)"
			prefix="$(ssh -T -o BatchMode=yes -- "$mt" "python3 -c 'import json,os; d=json.load(open(os.path.expanduser(\"~/.config/valise/instances/$mi.json\"))); print((d[\"provenance\"].get(\"npm\") or {}).get(\"prefix\",\"\"))'")"
			case "$prefix" in "" | "$(ssh -T -o BatchMode=yes -- "$mt" 'echo $HOME')"/*) pass "$mt: instance record describes the target" ;; *) fail "$mt: record prefix $prefix is the source's" ;; esac
			# The clone it replaced is marked superseded, not deleted.
			old="$(V old list "$mt")"
			echo "$old" | grep -q "$mi-clone（已被取代）：.* 由 $mi 取代" && pass "$mt: $mi-clone marked superseded" || { fail "$mt: $mi-clone not marked superseded"; echo "$old"; }
			V seal "$mt" "$mi-clone" --tier soul >/dev/null 2>&1 && fail "$mt: sealed a superseded instance" || pass "$mt: superseded instance refuses to seal"
			V old cleanup "$mt" "$mi-clone" --yes >/dev/null && ! V old list "$mt" | grep -q "$mi-clone" \
				&& pass "$mt: old cleanup removed $mi-clone and its kept directory" || fail "$mt: old cleanup"
			unit_active "$mt" "$munit" && pass "$mt: $munit still active after cleanup" || fail "$mt: cleanup stopped $munit"
		else
			fail "$ms->$mt: migrate"; cat "$work/revive.out"
		fi
		# Leave the source as it was found: running.
		ssh -T -o BatchMode=yes -- "$ms" "systemctl --user start '$munit'"
	fi
fi

# ---------------------------------------------------------------- M5 -----
# Web entry and the login relay's port rule. A real authorization needs a
# person at the browser; it is recorded in DEV_PLAN.md, not repeated here.
#   VALISE_DRILL_ENTRY   host:instance:port of a running web entry
#   VALISE_DRILL_LOGIN   host:instance:method:callback port (loopback OAuth)
IFS=: read -r eh ei ep <<<"${VALISE_DRILL_ENTRY:-ubuntu-vm:openclaw:18789}"
if in_hosts "$eh"; then
	echo "== M5: web entry $ei on $eh"
	# The service may have just been restarted by the M4 steps.
	ssh -T -o BatchMode=yes -- "$eh" "for i in \$(seq 1 60); do timeout 1 bash -c '</dev/tcp/127.0.0.1/$ep' 2>/dev/null && exit 0; sleep 1; done; exit 1" \
		|| echo "   warning: nothing listens on $eh:$ep"
	V open "$eh" "$ei" --no-browser --idle 6s > "$work/open.out" 2>&1 &
	opid_bg=$!
	for i in $(seq 1 30); do grep -q '^入口：' "$work/open.out" && break; sleep 0.5; done
	url="$(sed -n 's/^入口：//p' "$work/open.out")"
	grep -q '✓ 隧道已建立' "$work/open.out" && grep -q '✓ 远端端口在监听' "$work/open.out" && grep -q '✓ 健康检查通过' "$work/open.out" \
		&& pass "$eh/$ei: tunnel, remote listening and health shown separately" || { fail "$eh/$ei: entry steps"; cat "$work/open.out"; }
	code="$(curl -s -o /dev/null -w '%{http_code}' "$url")"
	[ "$code" -ge 200 ] && [ "$code" -lt 500 ] && pass "$eh/$ei: entry reachable through the tunnel (HTTP $code)" || fail "$eh/$ei: entry HTTP $code"
	wait "$opid_bg" && grep -q '入口隧道已关闭' "$work/open.out" && pass "$eh/$ei: closed after idle timeout" || fail "$eh/$ei: idle close"
	lport="${url#http://127.0.0.1:}"; lport="${lport%%/*}"
	! lsof -nP -iTCP:"$lport" -sTCP:LISTEN >/dev/null 2>&1 && pass "$eh/$ei: local port released" || fail "$eh/$ei: local port still bound"

	python3 -m http.server "$ep" --bind 127.0.0.1 >/dev/null 2>&1 &
	blocker=$!; sleep 1
	V open "$eh" "$ei" --no-browser --idle 2s > "$work/open2.out" 2>&1
	grep -q "本机端口 $ep 被占用，改用" "$work/open2.out" && pass "$eh/$ei: entry moved to another local port" || { fail "$eh/$ei: entry port conflict"; cat "$work/open2.out"; }
	kill "$blocker"
fi
IFS=: read -r lh li lm lp <<<"${VALISE_DRILL_LOGIN:-ubuntu-vm:openclaw:openai-codex:1455}"
if in_hosts "$lh"; then
	echo "== M5: login relay port rule ($lm on $lh)"
	python3 -m http.server "$lp" --bind 127.0.0.1 >/dev/null 2>&1 &
	blocker=$!; sleep 1
	V login "$lh" "$li" --method "$lm" < /dev/null > "$work/login.out" 2>&1
	grep -q "本机端口 $lp 已被 .*占用；登录回调必须使用同一端口" "$work/login.out" \
		&& pass "$lh/$li: busy callback port is an error naming the process, never another port" || { fail "$lh/$li: busy port"; tail -5 "$work/login.out"; }
	kill "$blocker"
	left="$(ssh -T -o BatchMode=yes -- "$lh" 'ls ~/.local/state/valise/login/ 2>/dev/null | wc -l; pgrep -f "[v]alise-porter login-run" | wc -l')"
	[ "$(echo $left | tr -d ' ')" = "00" ] && pass "$lh: no login session or process left" || fail "$lh: leftovers after the login relay ($left)"
fi

# ------------------------------------------------------------- result -----
if [ "$failures" -eq 0 ]; then
	echo "ALL_PASS (failures=0)"
else
	echo "FAILURES (failures=$failures)"
	exit 1
fi
