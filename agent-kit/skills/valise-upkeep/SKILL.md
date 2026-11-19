---
name: valise-upkeep
description: Create a rolling soul backup with Valise before risky changes. Use before saving, editing or deleting any memory (including remembering a new fact about the user), editing your persona, rules or instructions, installing or removing skills or plugins, changing configuration, upgrading your harness, or bulk-editing or cleaning up files.
---

# Back up before risky changes

Before any of the following, call `valise_seal` with a `reason` saying what you
are about to change:

- saving, editing or deleting memories — including remembering one new fact
  about the user — and editing your own persona, rules or instructions;
- installing or removing skills or plugins;
- changing configuration;
- upgrading the harness;
- editing or cleaning up many files at once.

Example reason: `"about to rewrite MEMORY.md: merge duplicate entries"`.

Always talk to the user in the user's language.

## Rules

- Keep memories only in your own memory files (`memory_files` and
  `handoff_targets` from `valise_status`). Never write into another agent's files,
  even on the same machine. If you have no memory file, tell the user instead of
  improvising one.
- `valise_seal` creates a rolling `soul`-tier backup (no credentials; `kind: auto`
  in the current date period, subject to period retention) and returns a snapshot
  id. Tell the user briefly (`"backed up as s_…; you can restore from it or pin
  it as a savepoint in Valise"`).
- `valise_seal` runs at most once every 10 minutes. If it returns `rate_limited` and
  cites a fresh `s_…` snapshot taken minutes ago (for example from a first-call
  safety-net backup covering your current workspace state), cite that snapshot id
  to the user and continue.
- If `valise_seal` fails for any other reason, stop, tell the user why, and ask
  whether to proceed without a backup. **Never skip backing up silently.**
- Do not call `valise_seal` for ordinary conversation or read-only lookups; only
  before making changes.
