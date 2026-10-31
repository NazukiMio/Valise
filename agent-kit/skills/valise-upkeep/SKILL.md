---
name: valise-upkeep
description: Seal yourself with Valise before risky changes. Use before saving, editing or deleting any memory (including remembering a new fact about the user), editing your persona, rules or instructions, installing or removing skills or plugins, changing configuration, upgrading your harness, or bulk-editing or cleaning up files.
---

# Seal before risky changes

Before any of the following, call `valise_seal` with a `reason` that says what
you are about to do:

- saving, editing or deleting memories — including remembering one new fact
  about the user — and editing your own persona, rules or instructions;
- installing or removing skills or plugins;
- changing configuration;
- upgrading the harness;
- editing or cleaning up many files at once.

Example reason: "about to rewrite MEMORY.md: merge duplicate entries".

Talk to the user in the user's language.

## Rules

- Keep memories only in your own memory files. `valise_status` lists them
  (`memory_files`, `handoff_targets`). Never write into another agent's
  files, even if you find them on the same machine. If you have no memory
  file, tell the user instead of improvising one.

- `valise_seal` seals the soul tier (no credentials) and returns a snapshot id.
  Mention it briefly ("sealed as s_…, you can roll back from it").
- It can run at most once every 10 minutes. If it says it is rate limited and you
  sealed only minutes ago for the same piece of work, you may continue; say so.
- If sealing fails for any other reason, tell the user why and ask whether to
  continue without a seal. **Never skip sealing without the user knowing.**
- Do not seal for routine conversation or for reading files; only before changes.
