---
name: valise-onboard
description: Onboard this agent's custom layout to Valise's server dossier and check backup coverage. Use right after installing Valise skills, after adding plugins or custom folders, or when asked to check backup coverage/bloat ("报备一下档案", "认识一下 Valise", "检查备份覆盖", "onboard Valise", "check backup coverage").
---

# Onboard layout to Valise

Always reply in the user's language. Only call the Valise MCP tools `valise_onboard_context` and (when needed) `valise_propose_dossier` (plus any runtime tool-discovery lookup such as `tool_search` to resolve prefixed tool IDs).
- **Deferred tool discovery**: If your runtime loads MCP tools lazily (e.g. `tool_search`), look up the prefixed real tool IDs for `valise_onboard_context` and `valise_propose_dossier` first. If these new tools are missing, stop immediately and ask the user to upgrade the server `valise-porter` and restart the gateway—never fall back to legacy tools.
- **Hard bans**: Never trigger a backup, never edit standing instructions (like `AGENTS.md`), never run shell commands, never read or list files on disk, and never include secrets, tokens, passwords, or file content snippets in any `reason` or reply.

## Steps

1. Call `valise_onboard_context` once. Read `instance`, `protection_plan`, `effective_rules` (including `local_roots` and `tool_deps`), `layout_facts`, `pending_proposals`, and `protection_summary`.
2. Deduplicate against `effective_rules` and `pending_proposals`. Using **only** `layout_facts` and facts you already know about your own setup, select **at most 12** high-value items across these 5 targets (do not assume `full` handles important `unclassified` persona/rule/core-config/memory files like `persona.md`—lightweight `soul` backups and clones omit `unclassified`, so propose them while still skipping trivial runtime noise):
   - **Runtime junk exclusion (`classify` -> `cache` / `ignore`)**: large (`>= 5 MiB`) or clearly rebuildable plugin caches, deps, or logs from `largest_dirs` / `unclassified_dirs` / `churn_files`. Never demote Profile `secrets`; `reason` must state why it is rebuildable.
   - **Plugin SQLite DBs (`sqlite`)**: databases from `untracked_sqlite` (or known plugin DBs inside `roots`) needing `VACUUM INTO` + integrity check. Never weaken Profile `secret_tables`; only set `secret_tables` if already known.
   - **In-root custom config, persona, or secrets (`classify` -> `secrets` / `identity` / `memory` / `workspace` / `state` / `skills` / `config`)**: newly spotted credential paths (`secrets`), custom persona/rule/memory files (e.g. `persona.md` -> `identity`), or custom config/memory/business directories so `soul`-tier clones/migrations keep them.
   - **Out-of-root personal asset/config dirs (`external_ref`)**: `external_symlinks` targets or important personal asset, memory, or config directories you know live **outside** `roots` (`ref_kind`: `external_dir` / `symlink_target`, with optional `suggested_category` in `identity`/`memory`/`workspace`/`state`/`skills`/`config`/`secrets`). The user chooses in Valise desktop whether to keep it as a reminder or promote it into backup (`local_roots`).
   - **Companion CLI tools (`tool_dep`)**: external CLI binaries your plugins/skills rely on (`name`, `purpose`, `reason`). Used read-only during migration to show an environment gap checklist; Valise never auto-installs or endorses arbitrary third-party tools.
3. If you have items, call `valise_propose_dossier` **once** with all items batched; if everything already matches, skip it. Never retry errors in a loop.
4. Reply concisely in 3-4 short points:
   - backup plan & schedule status (if unconfirmed/disabled, gently note it) + `protection_summary` **verbatim**;
   - proposals submitted and ask the user to review/approve them in Valise desktop (**健康 › 归档建议**) — or confirm none were needed;
   - any `external_ref` directories outside `roots` (noting they can be promoted into backup or kept as reminders) and companion `tool_dep` CLI tools.
