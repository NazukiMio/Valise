---
name: valise-backup
description: Check Valise backup status or take a rolling backup before risky self-edits. Use when the user asks about backups ("看看备份", "备份状态", "先备个份", "留个底", "backup status", "back up first") or before high-risk changes to core config, plugins/skills, persona/rules, memory, or bulk workspace files.
---

# Valise backup safety net

Always reply in the user's language. Only call `valise_status` and `valise_seal`.
- **Hard bans**: Never edit standing instructions (like `AGENTS.md`), never probe files on disk, and never put secrets, tokens, passwords, or file content snippets in `reason`.

## 1. Check backup status (`valise_status`)
Call `valise_status` once and answer in 2 short sentences:
- whether scheduled protection is enabled, active tier, and when the last backup ran;
- pass `protection_summary` **verbatim** (never infer off-site sync status from anything else).

## 2. Back up before high-risk changes (`valise_seal`)
Call `valise_seal` (with an optional short `reason`) **only** when the user asks for a backup, or right before high-risk self-edits: changing core config (e.g. `openclaw.json`), installing/removing skills or plugins, rewriting/cleaning persona, rules, or memory, bulk-editing/deleting workspace files, or upgrading the harness.
- **Never** call `valise_seal` for ordinary chat, read-only lookups, adding a single everyday memory fact, or minor tweaks to a single normal file.
- **Outcome & plan warning**: Report the snapshot id briefly (`s_...`, rolling `kind: auto`). If the response carries `plan_warning` (legacy `soul`-only instance), add one gentle sentence suggesting the user confirm a Protection Plan in Valise desktop so configs and workspace are covered.
- **Rate limit & errors**: If `rate_limited`, cite the returned `last_seal_id` and time honestly and proceed without retrying. On any other error, stop, state the reason in one sentence, and ask whether to continue. Never retry in a loop.
