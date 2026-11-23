# Doc & Source Research Checklist (Mode A)

Use this checklist when authoring a `valise-profile/1` profile purely from official documentation and/or source repositories without a live installation.

## 1. Eight-Item Evidence Checklist

| # | Area | What to Verify in Docs / Source | Profile Field(s) |
| :--- | :--- | :--- | :--- |
| 1 | **Binary & Version** | CLI executable name(s), private bin dir (if installed outside standard `PATH`), and a strictly read-only, offline version command | `detect.binaries`, `detect.bin_dirs`, `detect.version_argv` |
| 2 | **State Dir & Multi-Instance** | Default home/state dir (`~/.<name>` or XDG), env var override (`*_HOME` / `*_STATE_DIR`), reliable marker files, and named profile/bot layout + selector env var | `detect.state_dir_candidates`, `detect.markers_any`, `detect.home_env`, `detect.named`, `relocation`, `terms` |
| 3 | **Separate Roots** | Whether user workspace / project dir lives outside the state dir, and whether a read-only CLI command prints its resolved absolute path | `roots[].id`, `roots[].discover_argv`, `roots[].default` |
| 4 | **Secrets & Exclusive Creds** | Every file that stores API keys, OAuth tokens, bot/channel tokens, pairing keys, or configs that may inline `api_key`; identify which tokens cannot be cloned across devices | `paths` (`category: secrets`), `exclusive_secrets` |
| 5 | **SQLite & Secret Tables** | All SQLite `.db`/`.sqlite` files, WAL usage, and whether any database mixes credentials with sessions/state (inspect SQL schema / migrations for token/key/auth tables) | `consistency.sqlite[]`, `secret_tables`, `consistency.quiesce` |
| 6 | **Program Body vs State** | How the harness is installed (`npm_global` / `git` / `uv_tool` / `pipx` / `binary`), and whether code/runtime dirs live inside the state dir (must be `<literal-dir>/**`) | `provenance`, `body.paths`, `body.fresh_install_doc` |
| 7 | **Service, Entry, CLI & Starter Actions** | Official systemd `--user` unit name and service install command, localhost web UI/dashboard port/path/token readout, read-only health endpoints, onboarding/session CLI commands, and safe starter actions (`status` / `restart` / `doctor`). Include doc/source-verified entries/CLI/actions marked `verify: pending` rather than leaving an empty card (do not put on-demand dashboard ports in `health.checks`) | `service.candidates`, `service.install_argv`, `service.repair_action`, `entries`, `health`, `native_cli`, `native_session_cli`, `actions` |
| 8 | **Agent Kit (Skills & MCP)** | User-level skills directory and official MCP server registration/removal CLI commands (previewed & user-confirmed over admin connection, audited as `admin`) or config snippet | `agent_kit.skills_dir`, `mcp_config_doc`, `mcp_add_argv`, `mcp_remove_argv`, `mcp_config_snippet` |

## 2. Source Code Search Keywords (Node / Python / Go / Rust)

When grepping a harness repository, search these patterns to find exact paths, env vars, and secret stores:

- **State dir & env overrides**:
  - Node/TS: `os.homedir()`, `process.env.`, `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `STATE_DIR`, `_HOME`
  - Python: `Path.home()`, `os.environ.get`, `os.getenv`, `platformdirs`, `appdirs`
  - Go: `os.UserHomeDir`, `os.UserConfigDir`, `os.Getenv`
  - Rust: `dirs::home_dir`, `dirs::config_dir`, `std::env::var`, `directories::ProjectDirs`
- **Credentials & tokens (conservative whitelist)**:
  - `api_key`, `apiKey`, `API_KEY`, `.env`, `auth.json`, `credentials`, `secrets`, `token`, `oauth`, `keyring`, `vault`, `pairing`, `bot_token`
- **SQLite databases & migrations**:
  - `.sqlite`, `.db`, `CREATE TABLE`, `PRAGMA journal_mode`, `better-sqlite3`, `sqlite3`, `aiosqlite`, `rusqlite`, `modernc.org/sqlite`
- **Services, ports & web UI**:
  - `systemd`, `.service`, `127.0.0.1`, `localhost`, `listen(`, `PORT`, `gateway`, `dashboard`
- **Skills & MCP integration**:
  - `skills`, `mcp_servers`, `mcpServers`, `mcp add`, `stdio`, `SKILL.md`

## 3. Sample Path List Template for `--classify`

Synthesize a realistic sample file list (`sample-paths.txt`) from the harness's documented directory tree and run `valise profiles lint --strict --commands --classify sample-paths.txt <id>.yaml` (or `./scripts/verify-profile.sh --strict --sample sample-paths.txt <id>.yaml`):

```text
# Lines starting with '#' and blank lines are ignored.
# Format: <root>:<relative-path> (omitting "<root>:" defaults to "state").

# --- state root ---
.env
config.yaml
state/app.sqlite
state/app.sqlite-wal
sessions/2026-11-21.jsonl
skills/my-skill/SKILL.md
cache/http/index.json
logs/gateway.log

# --- nested body dir (if any) ---
runtime/node/bin/node

# --- additional root (only if declared in roots[]) ---
workspace:SOUL.md
workspace:MEMORY.md
workspace:notes/project.md
```

Check the simulation output to verify:
1. Every secret file lands in `secrets` (and `exclusive_secrets` where expected), never in `cache`/`ignore`/`body`.
2. SQLite files and their `-wal`/`-shm` sidecars are detected together.
3. No rule is unintentionally shadowed or left unused due to a glob typo.
4. Uncertain/extra files remain `unclassified` and appear in `full` / `soul-secrets` tiers.
