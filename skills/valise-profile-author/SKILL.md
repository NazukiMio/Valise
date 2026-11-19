---
name: valise-profile-author
description: Author, audit, or update a Valise `valise-profile/1` harness profile YAML. Use when the user asks to add support for an agent/harness in Valise, write `~/.config` local profile YAML, fix `profilelint` errors, or audit `verify: pending` items ("给某 harness 写一份 Valise profile", "生成 Valise 资料档案", "audit valise profile", "为新 Agent 写接入规则").
---

# Author a Valise Harness Profile (`valise-profile/1`)

Valise keeps all harness-specific knowledge inside declarative YAML (`profiles/*.yaml` for official packs, or `<ValiseDataDir>/profiles/<id>.yaml` for local unreviewed profiles). Go code never hardcodes harness names, paths, ports, or commands.

Before writing or editing YAML, consult [references/schema-v1.md](references/schema-v1.md) and start from [references/minimal-template.yaml](references/minimal-template.yaml).

## Hard Discipline Rules

1. **Read-only reconnaissance only**: Inspect documentation, CLI `--help`, config schemas, and read-only directory listings. Never modify the target harness's configuration, state, or running service just to draft a profile.
2. **Never read or echo secrets**: Locate credential filenames by schema/documentation only; never print API keys, tokens, `.env` contents, or private key files.
3. **Never fabricate paths or commands**: Any path, SQLite column, command flag, or recovery behavior not directly observed on a real installation or official version-pinned docs **must** carry `verify: pending` (field on `paths` / `actions` / `approval_check` / `identity`, or `# verify: pending` YAML comment) or remain omitted/`null`. Keep `status: draft` whenever any `verify: pending` marker exists.
4. **Whitelist secrets explicitly and avoid conflicts**: Declare credentials only under `category: secrets` (and `exclusive_secrets` when token/device identity must not blindly be cloned). A `secrets` glob must never also match `body`, `cache`, or `ignore` (including `body.paths`). Use `consistency.sqlite[].secret_tables` when a SQLite file stores both credentials and sessions/state.
5. **Argv arrays only, never shell strings**: Every command (`version_argv`, `discover_argv`, `steps`, `actions[].argv`, `entries[].auth.*`, `entries[].port_change.steps`, `health.checks[].argv`, `approval_check.argv`, `identity.argv`, `agent_kit.*_argv`) must be a YAML argv list, respecting exact variable substitution rules (`${HOME}`, `${STATE_DIR}`, `${PORT}`, `${OUT}`, `${PORTER}`, `${INSTANCE}`, `${STAGE_DIR}`). Never emit `curl | bash` or `sh -c`.
6. **Honest per-capability status**: Set `capabilities.<name>.status` to `tested` **only** with exact `versions` and `env` from real-machine runs (`backup`, `restore`, `install`, `manage`, `login`, `checkup`, `kit`); otherwise leave `untested` or `partial`.

## Workflow

### Step 1: Read-Only Reconnaissance
Collect facts about the target harness (`<harness>`) from official docs or an inspected target environment without changing anything:
- Executable binary name(s), private bin dirs (if any), and non-network version command (`--version`);
- State directory env override (`home_env`, `relocation.env`), default path (`~/.harness`), and unambiguous `markers_any`;
- Multi-instance glob pattern (`detect.named`), selector env vars (`${NAME}`, `${STATE_DIR}`), and whether named instances share one gateway service (`service: shared`);
- Separate roots beyond `state` (e.g. `workspace` via `discover_argv` or `default`);
- File map split honestly into Valise categories (`secrets`, `identity`, `memory`, `skills`, `config`, `sessions`, `state`, `body`, `cache`, `ignore`);
- Exact installation provenance (`npm_global`, `git`, `uv_tool`, `pipx`, or `binary`);
- Optional runtime details: systemd user unit (`scope: user`), local web entry (`127.0.0.1` port/path & auth token readout), read-only `health` checks, `agent_kit` skills directory + MCP add/remove command or snippet (and optional deprecated compatibility blocks `checkup` / `handoff` only if targeting legacy session governance).

### Step 2: Draft from the Minimal Template
Copy [references/minimal-template.yaml](references/minimal-template.yaml) and fill in only verified facts plus explicitly marked `verify: pending` hypotheses where needed for initial discovery/classification. Follow [references/schema-v1.md](references/schema-v1.md) strictly (`profilelint` uses YAML `KnownFields(true)` and rejects any unknown key, including mistyped fields like `identity.path` instead of `identity.name`).

### Step 3: Lint Strictly
Run Valise's linter against the new file (and its parent YAML if writing a child profile with `extends`):
- Using a built Valise CLI:
  ```bash
  valise profiles lint [--json] <path/to/harness.yaml>
  ```
- Inside the Valise Go repository:
  ```bash
  cd core && go run ./cmd/profilelint ../profiles/<harness>.yaml
  ```
Fix every reported issue until exit code is `0`. Inspect the `pending=<N>` count to ensure `status: verified` is never set while `pending > 0`.

### Step 4: Dry-Run Local Profile & Enrollment Report (Read-Only)
1. Copy the validated YAML into `<ValiseDataDir>/profiles/<id>.yaml` (on macOS `~/Library/Application Support/valise/profiles/`, on Linux `${XDG_DATA_HOME:-~/.local/share}/valise/profiles/`).
2. Confirm Valise loads it cleanly without rejection:
   ```bash
   valise profiles status
   ```
3. Run read-only discovery/enrollment against a target host (do NOT pass `--accept` yet until the user reviews the classification):
   ```bash
   valise enroll <server> --profile <id> --report-json
   ```
4. Inspect `unclassified` files, unexpected symlinks, SQLite sidecars, or missing markers from the JSON report and refine `paths` / `consistency` / `exclusive_secrets` without guessing secrets.

### Step 5: Evidence & Auditability
Record source provenance in YAML comments on every rule (`DOCS: <url>`, `OBS: <version/command/env>`) so reviewers or future harness updates can audit why each glob, SQLite column, or command was classified that way.

### Step 6: Contribution Checklist (For Official PRs to `profiles/*.yaml`)
Before submitting to upstream `profiles/`:
- Exact harness version tested (`harness_versions_tested`);
- Output of `valise enroll --report-json` (credential paths redacted if sensitive);
- OS/arch & install layout used for verification;
- Explicit evidence (official doc link or real install observation) for every removed `verify: pending`.
