# `valise-profile/1` Schema Reference

All top-level and nested fields are decoded with `yaml.Decoder.KnownFields(true)` — any unknown or misspelled key fails immediately (for example, `identity` uses `name`, NOT `path`).

## 0. Scope Boundary: Profile vs Dossier

- **Profile** defines universal, cross-machine rules (`detect`, `roots`, explicit `secrets`/`exclusive_secrets`, `body.paths`, `provenance`, `consistency.sqlite`, high-confidence `cache`/`ignore`, `service`/`entries`/`health`/`native_cli`/`agent_kit`).
- **Dossier (`valise-onboard`)** records per-machine facts (`local_overrides`, `local_sqlite`, `external_refs`). Default `full` backups preserve unclassified files inside `roots` in the sensitive layer, so leave uncertain or user-specific paths out of the Profile.

## 1. Metadata & Child Inheritance

- `schema`: `"valise-profile/1"` (required)
- `id`: lowercase `[a-z0-9]+(-[a-z0-9]+)*` (required; local profile YAML may explicitly override a same-ID official pack profile, always badged as local/unreviewed with one-click restore)
- `display_name`: human-readable name (required)
- `profile_version`: positive integer; increment on every edit
- `status`: `draft` | `verified` (`verified` forbids any `verify: pending` field/comment and requires non-empty `harness_versions_tested`)
- `harness_versions_tested`: list of exact verified versions
- `docs`: reference documentation URL (informational only; Valise never fetches it)
- `capabilities`: `{ install|manage|backup|restore|login|kit: { status: tested|partial|untested|failed, versions, env, date, note } }` (`tested` requires `versions` and `env`; recovery gate checks `capabilities.restore` only, not overall `status: draft`)
- `display` (optional): `{ color: "#RRGGBB", icon (<=12x24 single-width), icon_compact (<=6x12 single-width), icon_source, logo: { svg (<=64KiB safe SVG), source, license } }`
- `terms`: `{ agent }` (<= 20 chars, harness's own term for an agent, e.g. `profile`)
- `extends`: parent `id` (child profiles may ONLY set: `schema`, `id`, `display_name`, `profile_version`, `status`, `harness_versions_tested`, `docs`, `extends`, `when`, `paths`, `consistency.sqlite`, and legacy `checkup`)
- `when`: required when `extends` is set; `{ exists: { root: <root>, glob: <glob> } }`

## 2. Detection & Roots

- `detect`:
  - `binaries`: executable names
  - `version_argv`: read-only argv array (`${HOME}`, `${STATE_DIR}`)
  - `bin_dirs`: private executable dirs prepended to `PATH`, templated with `${HOME}`
  - `state_dir_candidates`: non-empty list using `${HOME}` or `${env:VAR}`
  - `markers_any`: relative non-pattern marker filenames under state dir
  - `home_env`: env var overriding `${HOME}` for candidates/named globs
  - `named`: `[{ glob, service, env }]`; last segment of `glob` has a single `*`, base uses `${HOME}`; `service` is `shared` or omitted; `env` values only use `${NAME}` / `${STATE_DIR}`
- `roots`: additional roots beyond implicit `state`
  - `id`: `[a-z0-9-]+`, unique, not `"state"`
  - `default`: path templated with `${STATE_DIR}` / `${HOME}`
  - `discover_argv`: optional read-only command printing a single absolute path (`${HOME}`, `${STATE_DIR}`)
- `relocation`: `null` or `{ env: ENV_VAR }`

## 3. Paths, Secrets & Consistency

- `paths`: `[{ root, glob, category, note, verify }]`
  - Valid `category`: `identity`, `memory`, `skills`, `config`, `sessions`, `state`, `body_patch`, `secrets`, `workspace`, `body`, `cache`, `ignore`
  - Evaluation order: **`body.paths` and `secrets` take priority**; other rules match top-to-bottom (first match wins).
  - `verify`: only `pending` or omitted
  - Prefer declaring body dirs solely under `body.paths` (duplicating `category: body` in `paths` triggers `redundant_body_path`).
- `exclusive_secrets`: `[{ root, glob }]` (device tokens, paired channel creds excluded when cloning; should be covered by a `category: secrets` rule)
- `consistency`:
  - `sqlite`: `[{ root, glob, secret_tables: [ident...] }]` (`secret_tables` stripped when sealing `soul`; table names `[A-Za-z_][A-Za-z0-9_]*`)
  - `quiesce`: `none` | `stop_service`

## 4. Body & Provenance

- `provenance` (required): `kind` is one of:
  - `npm_global`: `{ npm: { package, roots: [optional node_modules paths using ${HOME}/${STATE_DIR}] } }`
  - `git`: `{ git: { root, path } }`
  - `uv_tool`: `{ uv: { package } }`
  - `pipx`: `{ pipx: { package } }`
  - `binary`: `{ binary: { name } }` (command name only, no `/`)
- `body`:
  - `paths`: `[{ root, glob }]` — **EVERY `glob` MUST be `<literal-dir>/**`** (non-empty literal directory prefix without wildcards, followed by `/**`)
  - `bootstrap_argv`: argv list run inside body dir after git rebuild (`${HOME}`, `${STATE_DIR}`)
  - `fresh_install_doc`: reference URL (displayed only)

## 5. Runtime, Entries, Health, Actions & Agent Kit

- `service`: `{ candidates: [{ scope: user, unit }], repair_action, install_argv }` (`repair_action` requires non-empty `candidates` and references a non-interactive, non-secret `manage` action)
- `entries`: `[{ id, kind: web, host: <127.0.0.1|::1|localhost>, port: <1..65535>, path: </...>, auth, port_change }]`
  - `auth`: at most one of `url_argv` or `token_argv`; `url_field` (dotted path) requires `url_argv`; `url_suffix` (`${TOKEN}` only) requires `token_argv`; `hint`
  - `port_change`: `{ steps: [argv using ONLY ${PORT}], restart_service: bool }`
- `health`: `{ grace_seconds: >=0, checks: [ {kind: systemd_active} | {kind: tcp, entry} | {kind: http, entry, expect_status: [...]} | {kind: argv, argv, ok_exit} ] }`
- `login`: `[{ id, display_name, kind: loopback_oauth|device_code|paste_code|remote_browser, argv, url_pattern, timeout_seconds }]`
- `native_cli`, `native_session_cli` (argv using `${HOME}`), `native_hints` (requires `native_cli`)
- `native`: `{ export_argv }` (argv using `${HOME}`, `${STATE_DIR}`, `${OUT}`)
- `actions`: `[{ id, name, explain, risk: read|manage|config|danger, undo, argv, output: text|none|secret, secret, terminal, steps, transaction, verification, verify }]`
  - `argv` allows ONLY `${HOME}` and is mutually exclusive with `steps`/`transaction`; `config`/`danger` require `undo`; `output: secret` requires `secret` and forbids `terminal: true`.
  - B03 transactions (`steps` 1–16 + `transaction`): `risk: config`, `output: none`, `terminal: false`, `transaction.root: state`, `env` maps vars only to `${STAGE_DIR}[/safe/rel]`, `writes` lists relative files/dirs, optional `scratch`. Do not fabricate without live verification.
- `approval_check`: `{ argv (${HOME}), each, bypass_when, ask_when, default_when, remedy, repair_action, repair_expect, verify }`
- `identity`: `{ argv (${HOME}, ${STATE_DIR}), each, pick, name (required), verify }`
- `agent_kit`: `{ skills_dir: { root, path }, mcp_config_doc, mcp_add_argv (${PORTER}, ${INSTANCE}), mcp_config_snippet (${PORTER}, ${INSTANCE}), mcp_remove_argv (${INSTANCE}) }` (`mcp_add_argv` / `mcp_remove_argv` are previewed and user-confirmed executed over a dedicated admin connection during kit install/uninstall, with manual commands/snippets kept in a collapsible fallback)
- `install`: `{ id, summary, where, takes, notes, requires: [{binary, version: ">=x.y.z", why, install: [{os_id, argv}]}], steps: [npm_global | script | argv | path_add] }`
  - `script` step requires `https://` `url`, 64-hex `sha256`, `interpreter` (e.g. `[bash]`), and `runtime_downloads` (`""` when verified none). Never fabricate hashes.

## 6. Executable Command Audit (15 Categories / 18 Fields) & Variable Whitelist

`profilelint --commands` extracts all server-side executable/install entries:

| # | Field(s) | Tier | Allowed Placeholders |
| :--- | :--- | :--- | :--- |
| 1 | `detect.version_argv` | `daily` | `${HOME}`, `${STATE_DIR}` |
| 2 | `roots[].discover_argv` | `daily` | `${HOME}`, `${STATE_DIR}` |
| 3 | `body.bootstrap_argv` | `owner` | `${HOME}`, `${STATE_DIR}` |
| 4 | `service.install_argv` | `admin` | `${HOME}`, `${STATE_DIR}` |
| 5 | `entries[].auth.url_argv` / `token_argv` | `owner` | `${HOME}`, `${STATE_DIR}` |
| 6 | `entries[].port_change.steps[]` | `admin` | `${PORT}` only |
| 7 | `health.checks[].argv` | `daily` | `${HOME}`, `${STATE_DIR}` |
| 8 | `login[].argv` | `owner` | `${HOME}`, `${STATE_DIR}` |
| 9 | `native.export_argv` | `daily` | `${HOME}`, `${STATE_DIR}`, `${OUT}` |
| 10 | `native_cli`, `native_session_cli` | `admin` | `${HOME}` only |
| 11 | `actions[].argv`, `actions[].steps[].argv` | per `risk` | `${HOME}` (B03 env uses `${STAGE_DIR}`) |
| 12 | `approval_check.argv` | `daily` | `${HOME}` only |
| 13 | `identity.argv` | `daily` | `${HOME}`, `${STATE_DIR}` |
| 14 | `install.requires[].install[].argv`, `install.steps[]` (`argv`/`npm_global`/`script`/`path_add`) | `admin` | `${HOME}` |
| 15 | `agent_kit.mcp_add_argv`, `agent_kit.mcp_remove_argv` | `admin` | `${PORTER}`, `${INSTANCE}` (`mcp_remove_argv`: `${INSTANCE}`) |

## 7. Lint Hard Errors vs Soft Warnings

### Hard Errors (Block Packing / Loading / Import)
- Unknown YAML fields (`KnownFields(true)`), relative-path violations (`..`, non-templated absolute paths).
- `body.paths[].glob` is not `<literal-dir>/**`.
- `paths` has `category: body` whose `(root, glob)` is missing from `body.paths`.
- `secrets`, `exclusive_secrets`, or `consistency.sqlite` overlaps `body`, `cache`, or `ignore` (including `body.paths`).
- Any executable `argv` invokes a shell wrapper (`sh -c`, `bash -lc`, `zsh -ec`, etc., even behind `env`/`sudo`/`nohup`) or contains command substitution `$(` or backticks `` ` ``.
- `status: verified` with any `verify: pending` or empty `harness_versions_tested`; `capabilities.*.status: tested` missing `versions` or `env`.

### Soft Warnings (Fail When `--strict` Is Passed)
- `deprecated_field`: uses `checkup`, `handoff`, or `capabilities.checkup`.
- `redundant_body_path`: `paths` duplicates a `(root, glob)` already in `body.paths`.
- `shadowed_path`: an earlier broader non-`secrets` rule shadows a later non-`secrets` rule under the same `root`.
- `exclusive_not_in_secrets`: `exclusive_secrets` entry is not covered by any `category: secrets` rule under the same `root`.
- `missing_capabilities`: top-level profile omits `capabilities`.
- `missing_secrets`: top-level profile has no `category: secrets` rule.

## 8. Deprecated Legacy Fields (DO NOT USE in New Profiles)

Retained solely for backward compatibility with older published profiles (memory/session governance is deferred to ValiseMemo / v0.2+; triggers `deprecated_field` under `--strict`):
- `checkup`: `{ sessions, thresholds, remedies, memory_files, memory_mechanism_markers, native_options_doc }`
- `handoff`: `{ targets: [{ root, glob, mode: append_section, max_lines }], new_session_hint }`
- `capabilities.checkup`
