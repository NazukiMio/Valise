# `valise-profile/1` Schema Reference (Verified Against `core/profile/`)

All top-level fields are parsed with `yaml.Decoder.KnownFields(true)` — any misspelled or undocumented key fails immediately.

## 1. Metadata & Child Inheritance
- `schema`: `"valise-profile/1"` (required)
- `id`: lowercase `[a-z0-9]+(-[a-z0-9]+)*` (required, unique across pack/local)
- `display_name`: human-readable name (required)
- `profile_version`: positive integer; increment on every profile edit
- `status`: `draft` or `verified` (`verified` forbids any `verify: pending` field or comment and requires non-empty `harness_versions_tested`)
- `harness_versions_tested`: list of exact versions tested
- `docs`: reference documentation URL (informational only; Valise never fetches it)
- `extends`: parent `id` (child profiles may ONLY set: `schema`, `id`, `display_name`, `profile_version`, `status`, `harness_versions_tested`, `docs`, `extends`, `when`, `paths`, `consistency.sqlite`, `checkup.{memory_files,memory_mechanism_markers}`)
- `when`: required when `extends` is set; `{ exists: { root: <root>, glob: <glob> } }`

## 2. Detection & Roots
- `detect`:
  - `binaries`: executable names
  - `version_argv`: read-only argv array (`${HOME}`, `${STATE_DIR}`)
  - `bin_dirs`: directories prepended to `PATH`, templated with `${HOME}`
  - `state_dir_candidates`: non-empty list using `${HOME}` or `${env:VAR}`
  - `markers_any`: relative non-pattern marker filenames under state dir
  - `home_env`: env var overriding `${HOME}` for candidates/named globs
  - `named`: `[{ glob, service, env }]`; last segment of `glob` must have a single `*`, base uses `${HOME}`; `service` is `shared` or omitted; `env` values only use `${NAME}` / `${STATE_DIR}`
- `roots`: additional roots beyond implicit `state`
  - `id`: `[a-z0-9-]+`, unique, not `"state"`
  - `default`: path templated with `${STATE_DIR}` / `${HOME}`
  - `discover_argv`: optional read-only command printing a single absolute path (`${HOME}`, `${STATE_DIR}`)
- `relocation`: `null` or `{ env: ENV_VAR }`

## 3. Paths & Consistency
- `paths`: `[{ root, glob, category, note, verify }]`
  - Valid `category`: `identity`, `memory`, `skills`, `config`, `sessions`, `state`, `body_patch`, `secrets`, `workspace`, `body`, `cache`, `ignore`
  - `secrets` globs MUST NOT overlap `body`, `cache`, `ignore`, or `body.paths`
  - `verify`: only `pending` or omitted
- `exclusive_secrets`: `[{ root, glob }]` (device tokens, paired channel creds that must not carry over to clones)
- `consistency`:
  - `sqlite`: `[{ root, glob, secret_tables: [ident...] }]` (`secret_tables` entries stripped when sealing `soul`)
  - `quiesce`: `none` | `stop_service`

## 4. Body & Provenance
- `provenance` (required): `kind` is one of:
  - `npm_global`: `{ npm: { package, roots: [optional node_modules paths using ${HOME}/${STATE_DIR}] } }`
  - `git`: `{ git: { root, path } }`
  - `uv_tool`: `{ uv: { package } }`
  - `pipx`: `{ pipx: { package } }`
  - `binary`: `{ binary: { name } }` (command name only, no `/`)
- `body`: `{ paths: [{ root, glob }], bootstrap_argv: [...], fresh_install_doc: <url> }`

## 5. Runtime, Entries, Health & Actions
- `service`: `{ candidates: [{ scope: user, unit }], repair_action, install_argv }` (`repair_action` requires non-empty `candidates` and references a non-interactive, non-secret `manage` action)
- `entries`: `[{ id, kind: web, host: <IP|localhost>, port: <1..65535>, path: </...>, auth, port_change }]`
  - `auth`: at most one of `url_argv` or `token_argv`; `url_field` (dotted path) requires `url_argv`; `url_suffix` (uses `${TOKEN}` only) requires `token_argv`; `hint`
  - `port_change`: `{ steps: [argv using ONLY ${PORT}], restart_service: bool }`
- `health`: `{ grace_seconds: >=0, checks: [ {kind: systemd_active} | {kind: tcp, entry} | {kind: http, entry, expect_status: [...]} | {kind: argv, argv, ok_exit} ] }`
- `login`: `[{ id, display_name, kind: loopback_oauth|device_code|paste_code|remote_browser, argv, url_pattern, timeout_seconds }]`
- `actions`: `[{ id, name, explain, risk: read|manage|config|danger, undo, argv, output: text|none|secret, secret, terminal, steps, transaction, verification, verify }]`
  - `argv` allows ONLY `${HOME}` and is mutually exclusive with `steps`/`transaction`
  - `config` and `danger` actions require non-empty `undo`
  - `output: secret` requires `secret: { json_field, url_param }` and forbids `terminal: true`
  - B03 transactions (`steps` 1–16 + `transaction`): `risk: config`, `output: none`, `terminal: false`, `transaction.root: state`, `env` maps vars only to `${STAGE_DIR}[/safe/rel]`, `writes` lists relative files/dirs
- `approval_check`: `{ argv (${HOME}), each, bypass_when, ask_when, default_when, remedy, repair_action, repair_expect, verify }`
- `identity`: `{ argv (${HOME}, ${STATE_DIR}), each, pick, name (required), verify }` (NOTE: there is NO `path` key!)
- `terms`: `{ agent }` (<= 20 chars)

## 6. Interactive, Install, Checkup & Agent Kit
- `native_cli`, `native_session_cli` (argv using `${HOME}`), `native_hints` (requires `native_cli`)
- `native`: `{ export_argv }` (argv using `${HOME}`, `${STATE_DIR}`, `${OUT}`)
- `checkup`:
  - `sessions`: `{ root, glob, format: jsonl|sqlite, usage_fields, time_field, sqlite: { table, session_column, time_column, time_unit: s|ms, tokens_column, tokens_json: {column, path}, size_column, bytes_columns, usage: {table, session_column, tokens_columns, time_column, time_unit} } }`
  - `thresholds`, `remedies` (keys: `long_session`, `cost_growth`, `memory_bloat`, `session_pileup`, `no_memory_mechanism`), `memory_files` (`warn_lines > 0`), `memory_mechanism_markers`, `native_options_doc`
- `handoff`: `{ targets: [{ root, glob, mode: append_section, max_lines > 0 }], new_session_hint }`
- `agent_kit`: `{ skills_dir: { root, path }, mcp_config_doc, mcp_add_argv (${PORTER}, ${INSTANCE}), mcp_config_snippet (${PORTER}, ${INSTANCE}), mcp_remove_argv (${INSTANCE}) }`
- `install`: `{ id, summary, where, takes, notes, requires: [{binary, version: ">=x.y.z", why, install: [{os_id, argv}]}], steps: [npm_global | script | argv | path_add] }`
  - `script` step requires `https://` `url`, 64-hex `sha256`, `interpreter`, `runtime_downloads` (`""` when verified none)
- `capabilities`: `{ install|manage|backup|restore|login|checkup|kit: { status: tested|partial|untested|failed, versions, env, date, note } }` (`tested` requires `versions` and `env`)
