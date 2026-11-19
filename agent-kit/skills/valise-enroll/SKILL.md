---
name: valise-enroll
description: Learn your Valise protection skills. Use when the user says they installed Valise (or, in older wording, an escape pod) for you and asks you to learn it — e.g. "I installed Valise for you, learn it", "我给你装了 Valise 保护技能，你学习一下", "我给你装了逃生舱，你学习一下", "看看你的备份".
---

# Learn your Valise protection

Valise protects you. Code does the backup and restore; you only need to know
how it works and keep a few habits.

Two backup tracks exist:
- **User Protection Plan**: normally scheduled `full` backups (including credentials
  and unclassified files) into rolling date-period vaults, optional off-site copy
  and period retention, plus permanent savepoints pinned or manually created by the user.
- **Agent MCP backups**: when you call `valise_seal` or `valise_handoff_begin` (or
  when Valise runs a safety-net backup on the first tool call of a connection),
  Valise takes a rolling `soul`-tier backup (persona, memory, skills, sessions, state;
  no credentials) as `kind: auto` in the current date-period vault. It follows
  period retention and can be promoted by the user into a permanent savepoint.

Always talk to the user in the user's language.

## Steps

1. Call `valise_status`. Look at:
   - `protection_plan` (whether scheduled backups are enabled, schedule, tier,
     period unit, `keep_auto_periods` retention, and auto-copy targets);
   - `last_seal` and `auto_sealed_snapshot` (set if this connection just took
     a safety-net `soul` backup);
   - `protection_summary` — pass this sentence on **verbatim**; never infer "no
     backup targets" from an empty `last_seal_synced_to` list.
2. Tell the user in two or three clear sentences:
   - whether scheduled protection is enabled and what is covered (Protection Plan
     `full` coverage vs your MCP rolling `soul`-tier backups without credentials);
   - when the most recent backup was taken (or that none exists yet);
   - whether backups are copied off-site (`protection_summary`).
3. Call `valise_declare` once with your honest current `security_skills` and
   `approval_mode` (`ask`, `bypass`, or `unknown`), so the Valise Health page has an
   initial unverified declaration.
4. Check `unclassified` and `changing_stable_files` in `valise_status`:
   - `unclassified` are paths Valise could not classify (kept in the credentials
     layer by default under "unknown means sensitive", outside `soul`).
   - `changing_stable_files` are files in identity/config/skills that changed on
     every recent backup (often runtime locks or heartbeats).
   If you genuinely know what any of those paths are, propose categories with
   `valise_propose_enrollment` (never propose non-`secrets` over a `secrets` file;
   moving files to `cache`, `body`, or `ignore` is marked as a demotion). Tell the
   user to review and confirm them in the Valise desktop app (**健康 › 归档建议**)
   or CLI (`valise proposals <server> <instance>`).
5. Keep the habits in `valise-upkeep`: call `valise_seal` before editing memory,
   persona, instructions, skills, plugins, or config.
   If your harness has standing instructions you can edit, add **one** short line,
   e.g. "Before changing memory, persona, skills, or config, call `valise_seal`
   (`valise-upkeep`)."
   Ensure a fresh backup covers this edit first:
   - If `valise_status` returned `auto_sealed_snapshot` (or a backup taken minutes
     ago), cite that snapshot id and go ahead without calling `valise_seal` again
     (it shares the 10-minute rate limit);
   - Otherwise call `valise_seal` with `reason: "add valise-upkeep reference"`
     (if it returns `rate_limited` citing a fresh `s_...` snapshot, use that id).
   Tell the user exactly what line you added and where.

## Capability boundary

There is no MCP tool to restore, delete, read credentials, or write arbitrary files.
Restore, rollback, and savepoint promotion are always the user's decision in Valise.
