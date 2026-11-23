---
name: valise-profile-author
description: Author, audit, or update a Valise `valise-profile/1` harness profile YAML from official docs/source repositories (Mode A) or read-only live inspection (Mode B). Use when the user asks to add a harness to Valise, draft a local profile YAML, fix `profilelint` errors/warnings, or audit `verify: pending` items ("给某 harness 写一份 Valise profile", "生成 Valise 资料档案", "audit valise profile", "为新 Agent 写接入规则").
---

# Author a Valise Harness Profile (`valise-profile/1`)

Valise stores all harness-specific rules in declarative YAML (`profiles/*.yaml` for signed packs, or local unreviewed profiles). Before drafting, read [references/schema-v1.md](references/schema-v1.md), [references/doc-research-checklist.md](references/doc-research-checklist.md), and start from [references/minimal-template.yaml](references/minimal-template.yaml).

## Core Boundary: Profile vs Dossier + `full` Fallback

- **Profile (`<id>.yaml`)** owns only cross-machine, universal rules: `detect`, `roots`, explicit `secrets` / `exclusive_secrets` whitelists, `body.paths`, `provenance`, core `consistency.sqlite` + `secret_tables`, high-confidence `cache`/`ignore`, `service`, `entries`, `health`, `native_cli`, and `agent_kit`.
- **Dossier (`valise-onboard`) + `full` fallback** own per-machine reality: user-installed plugins, custom workspace subdirs, and miscellaneous files. Under Valise's default `full` backup, unclassified files inside `roots` are safely preserved in the sensitive layer—never stuff speculative paths into a Profile or guess `cache`/`ignore`.

## Hard Discipline Rules (Core-First, Zero Fabrication)

1. **Read-only only & never read secrets**: Inspect docs, source code, CLI `--help`, and read-only directory listings. Never edit the target harness or read/print `.env`, API keys, tokens, or private keys.
2. **Never fabricate hashes, transactions, or deprecated blocks**:
   - **FORBIDDEN**: Fabricating `install.steps` `sha256`/`integrity` without actually downloading and verifying the artifact.
   - **FORBIDDEN**: Fabricating unverified B03 `actions` transactions (`steps` + `transaction`) or `approval_check` schemas without live machine verification.
   - **FORBIDDEN**: Emitting deprecated `checkup` or `handoff` blocks in any new profile.
3. **Strict `body.paths` & `secrets` rules**:
   - Every `body.paths[].glob` MUST be `<literal-dir>/**`; do not duplicate `category: body` in `paths`.
   - Declare credentials explicitly as `category: secrets` (and `exclusive_secrets` for non-cloneable bot/device tokens). Never overlap `secrets`, `exclusive_secrets`, or `consistency.sqlite` with `body`/`cache`/`ignore`.
4. **Argv arrays only**: Every executable command must be a YAML argv list using only allowed placeholders (`${HOME}`, `${STATE_DIR}`, `${PORT}`, `${OUT}`, etc.). Never emit shell strings, `sh -c`/`bash -lc`, `curl | bash`, `$(...)`, or backticks.
5. **Honest `verify: pending` & `capabilities`**: Mark every unverified rule with `verify: pending` (field or `# verify: pending` comment). Keep `status: draft` and set every capability not verified on a real host to `status: untested`.

## Two-Mode Workflow

### Mode A (Default): Official Docs / Source Repository Only
Use when no live installation is available (or user supplies docs/repo URL):
1. Work through the 8-item checklist and source search keywords in [references/doc-research-checklist.md](references/doc-research-checklist.md).
2. Populate all doc/source-proven core sections: `detect`, `roots`, `paths` (conservative `secrets` first), `exclusive_secrets`, `consistency.sqlite`, `provenance`, `body.paths` (`<dir>/**`), `service`, `entries`, `health`, `native_cli`, `native_session_cli`, safe starter `actions`, `agent_kit`, and `capabilities` (all `untested`).
   - **DO NOT omit doc/source-verified interactive entries or starter actions**: Whenever official docs or source confirm localhost web UI/dashboard ports (`entries`), onboarding CLI (`native_cli`), daily interactive session CLI (`native_session_cli`), service install command (`service.install_argv`), or safe starter `actions` (`status` / `restart` / `doctor`), include them and mark `# verify: pending` / `verify: pending` so users immediately get usable interactive controls after importing instead of an empty card! (Do not put on-demand dashboard ports into always-on `health.checks`.)
3. Annotate every key rule with `DOC: <page/url>` or `SRC: <file:symbol>` comments, and synthesize a `<sample.txt>` path list from docs/source for offline classification testing.

### Mode B (Optional): Read-Only Live Inspection
Use when the user has a live host or local install:
1. Run strictly read-only checks (`<bin> --version`, `--help`, directory listing, `systemctl --user list-unit-files`, config schema inspection). Never read secret file contents.
2. Record real-world observations with `OBS: <version/env/finding>` comments; only remove `verify: pending` or upgrade a capability to `partial`/`tested` (with exact `versions` + `env`) when actually verified.

## Offline Verification Loop

Validate strictly against the Valise linter and simulate path classification before handing off:

- **Standalone Valise CLI** (works anywhere):
  ```bash
  valise profiles lint --strict --commands --classify <sample.txt> <id>.yaml
  ```
  *(For a child profile using `extends`, pass `<parent.yaml> <id>.yaml`.)*
- **Inside the Valise repository**:
  ```bash
  ./scripts/verify-profile.sh --strict --sample <sample.txt> <id>.yaml
  ```

Fix all hard errors and unintended warnings (`shadowed_path`, `redundant_body_path`, `exclusive_not_in_secrets`, `missing_capabilities`, `missing_secrets`, `deprecated_field`) until exit code is `0`.

## Required Deliverables (3 Items)

Always deliver all three items in your final response:
1. **Validated `<id>.yaml`**: Strictly compliant with `valise-profile/1`, with `DOC:` / `SRC:` / `OBS:` provenance comments on every rule.
2. **Command Audit & Pending Summary**:
   - Complete list of executable commands extracted by `--commands` (noting permission tier and whether any basic red flags appeared);
   - Explicit list of all remaining `verify: pending` items and why each needs live verification.
3. **Import & Next-Step Guidance**:
   - **Desktop GUI (recommended)**: Import via 「设置 › 资料包 › 导入本地资料 (.yaml)…」 (previews capability diff when overriding a same-ID official profile and previews all executable commands before saving; same-ID local overrides are badged as local/unreviewed with one-click removal to restore official and one-click sync to enrolled instances);
   - **Manual local placement** (`os.UserConfigDir()`):
     - macOS: `~/Library/Application Support/valise/profiles/<id>.yaml`
     - Linux: `${XDG_CONFIG_HOME:-~/.config}/valise/profiles/<id>.yaml`
     Then run `valise profiles status` to confirm clean loading;
   - **Read-only live probe**: Run `valise enroll <server> --profile <id> --report-json` (without accepting yet) to inspect `unclassified` files and refine rules.
