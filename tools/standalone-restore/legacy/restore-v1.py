#!/usr/bin/env python3
"""DEPRECATED: frozen v1 compatibility fixture; use ../restore.py.

Restore a Valise snapshot without Valise.

Needs only Python 3 (standard library), and the `age` and `zstd` command-line
tools. Implements docs/VAULT_FORMAT.md (valise-vault/1):

    restore.py --vault DIR --identity identity.txt --list
    restore.py --vault DIR --identity identity.txt --snapshot s_... --out DIR [--include-secrets]

The vault tree (docs/VAULT_FORMAT.md section 10) keeps one complete vault per
period: point --vault at a period folder, e.g. valise-vault/<server>/<instance>/
2026-10-01_2026-10-31/. The pre-tree vault is one folder per server.

Get identity.txt from your recovery kit with `age -d -o identity.txt keystore.age`.
Files are written to OUT/<root>/<path> (the body patch to OUT/valise/body.patch)
with their recorded permissions and mtime, and every file's SHA-256 is checked.
Secrets and unclassified files ("unknown means sensitive") are skipped unless
--include-secrets is given; a raw (full instance) snapshot needs it to restore
at all.
"""

import argparse
import datetime
import hashlib
import json
import os
import subprocess
import sys


def die(msg):
    print("restore: " + msg, file=sys.stderr)
    sys.exit(1)


def run(argv, data):
    p = subprocess.run(argv, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if p.returncode != 0:
        die("%s failed: %s" % (argv[0], p.stderr.decode(errors="replace").strip()))
    return p.stdout


class Vault:
    def __init__(self, root, identity):
        self.root = root
        self.identity = identity
        with open(os.path.join(root, "VAULT"), "rb") as f:
            desc = json.load(f)
        if desc.get("format") != "valise-vault/1":
            die("not a valise-vault/1 directory: " + root)
        self.blobs = {}
        index_dir = os.path.join(root, "index")
        for name in sorted(os.listdir(index_dir)) if os.path.isdir(index_dir) else []:
            if not name.endswith(".json") or name.startswith("."):
                continue
            with open(os.path.join(index_dir, name), "rb") as f:
                ix = json.load(f)
            for b in ix["blobs"]:
                self.blobs[b["id"]] = b

    def decrypt(self, data):
        return run(["age", "-d", "-i", self.identity], data)

    def blob(self, blob_id):
        e = self.blobs.get(blob_id)
        if e is None:
            die("blob %s is not in any index" % blob_id[:12])
        pack = os.path.join(self.root, "packs", e["pack"][:2], e["pack"] + ".pack")
        with open(pack, "rb") as f:
            f.seek(e["offset"])
            ct = f.read(e["length"])
        if len(ct) != e["length"]:
            die("pack %s is truncated" % e["pack"][:12])
        plain = run(["zstd", "-d", "-q", "-c"], self.decrypt(ct))
        if len(plain) != e["plain_size"]:
            die("blob %s has the wrong size" % blob_id[:12])
        return plain

    def snapshot(self, sid):
        with open(os.path.join(self.root, "snapshots", sid + ".age"), "rb") as f:
            head = json.loads(self.decrypt(f.read()))
        if head.get("format") != "valise-snapshot/1" or head.get("snapshot_id") != sid:
            die("snapshot header is invalid")
        doc = b"".join(self.blob(b) for b in head["files_blobs"])
        if hashlib.sha256(doc).hexdigest() != head["files_sha256"]:
            die("file list does not match files_sha256")
        files = json.loads(doc)
        if files.get("format") != "valise-files/1":
            die("file list is invalid")
        return head, files


def safe_join(out, root, rel):
    parts = [root] + rel.split("/")
    if any(p in ("", ".", "..") for p in parts) or rel.startswith("/"):
        die("refusing unsafe path %s:%s" % (root, rel))
    return os.path.join(out, *parts)


def parse_time_ns(s):
    """RFC 3339 -> integer nanoseconds since the epoch (exact)."""
    s = s.replace("Z", "+00:00")
    frac_ns = 0
    if "." in s:
        head, rest = s.split(".", 1)
        digits = ""
        while rest and rest[0].isdigit():
            digits, rest = digits + rest[0], rest[1:]
        frac_ns = int((digits + "000000000")[:9])
        s = head + rest
    secs = int(datetime.datetime.fromisoformat(s).timestamp())
    return secs * 1_000_000_000 + frac_ns


def list_snapshots(root):
    snap_dir = os.path.join(root, "snapshots")
    for name in sorted(os.listdir(snap_dir)):
        if name.endswith(".meta.json"):
            with open(os.path.join(snap_dir, name), "rb") as f:
                m = json.load(f)
            print("%s  %-12s %-13s %s  %d files" % (m["snapshot_id"], m["instance_id"], m["tier"], m["created_at"], m["file_count"]))


def main():
    ap = argparse.ArgumentParser(description="Restore a Valise snapshot with age and zstd only.")
    ap.add_argument("--vault", required=True)
    ap.add_argument("--identity", required=True)
    ap.add_argument("--snapshot")
    ap.add_argument("--out")
    ap.add_argument("--include-secrets", action="store_true")
    ap.add_argument("--list", action="store_true", help="list snapshots (needs no key)")
    a = ap.parse_args()
    if a.list:
        list_snapshots(a.vault)
        return
    if not a.snapshot or not a.out:
        die("--snapshot and --out are required")
    if os.path.exists(a.out) and os.listdir(a.out):
        die("output directory is not empty: " + a.out)

    v = Vault(a.vault, a.identity)
    head, files = v.snapshot(a.snapshot)
    if head["tier"] == "raw" and not a.include_secrets:
        die("this is a raw (full instance) snapshot and is treated as secrets as a whole; "
            "add --include-secrets to restore it")
    restored = skipped = 0
    for f in files["files"]:
        if f["category"] in ("secrets", "unclassified") and not a.include_secrets:
            skipped += 1
            continue
        dest = safe_join(a.out, f["root"], f["path"])
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        if os.path.lexists(dest):
            die("%s:%s already exists in the output; the output file system is probably "
                "case-insensitive (restore onto a case-sensitive one, e.g. Linux)" % (f["root"], f["path"]))
        h = hashlib.sha256()
        with open(dest, "wb") as out:
            for b in f["blobs"]:
                data = v.blob(b)
                h.update(data)
                out.write(data)
        if h.hexdigest() != f["sha256"]:
            die("%s:%s does not match its sha256" % (f["root"], f["path"]))
        os.chmod(dest, f["mode"] & 0o7777)
        t = parse_time_ns(f["mtime"])
        os.utime(dest, ns=(t, t))
        restored += 1
    for s in files["symlinks"]:
        dest = safe_join(a.out, s["root"], s["path"])
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        if os.path.lexists(dest):
            die("%s:%s already exists in the output; the output file system is probably "
                "case-insensitive (restore onto a case-sensitive one, e.g. Linux)" % (s["root"], s["path"]))
        os.symlink(s["target"], dest)
    print("restored %d files and %d symlinks from %s (%s, %s) into %s"
          % (restored, len(files["symlinks"]), a.snapshot, head["instance_id"], head["tier"], a.out))
    if skipped:
        print("skipped %d secret or unclassified files (use --include-secrets to restore them)" % skipped)


if __name__ == "__main__":
    main()
