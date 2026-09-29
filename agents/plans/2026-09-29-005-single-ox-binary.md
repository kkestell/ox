# Single ox binary

## Goal

Ox ships as two binaries, `ox` and `ur`, each with its own global settings file.
After this change there is one binary, `ox`:

- `ox [--server <name>] [<directory>]` runs the Ox client.
- `ox run [--dir <path>] [--model <id>] [--effort <level>] <prompt>` runs a
  headless run.
- `ox acp` runs the Ox server over stdin and stdout.
- `ox auth login` and `ox auth logout` save or remove the OpenRouter API key.

One global settings file, `~/.config/ox/settings.json`, holds `servers`,
`favorites`, `model`, `effort`, and `mode`. The name Ur is gone from everything
a user or model sees.

## Related code

- `crates/ur/src/main.rs` — the hand-written `ur` argument parser, `--model` and
  `--effort` checks, and login and logout. Becomes the server library's entry
  functions.
- `crates/ox/src/main.rs` — the clap parser for the Ox client. Gains the
  subcommands.
- `crates/ox/src/config.rs` — reads `servers` and `favorites`, rejects unknown
  fields, honors `$XDG_CONFIG_HOME`, and builds the bundled server entry from
  the sibling `ur` executable.
- `crates/ur/src/settings.rs` — reads and saves `model`, `effort`, and `mode` in
  `~/.config/ur/settings.json` and `.ur/settings.json`. Already ignores unknown
  fields and keeps other fields when saving.
- `crates/ur/src/acp.rs:799` — the terminal auth method, whose arguments an ACP
  client appends to the server's configured command.
- `crates/ur/src/acp.rs:1017` — the agent name sent at initialization.
- `crates/ur/src/sessions.rs:21`, `crates/ur/src/sessions.rs:1060` — the
  database file and data directory.
- `crates/ur/src/auth.rs:4` — the keyring service name.
- `crates/ur/src/skills.rs:54` — the user skills directory.
- `crates/ur/src/prompts/system_prompt.md` — "You are Ur".
- `crates/ox/tests/tui.rs` — the tmux tests write the global settings file under
  `$XDG_CONFIG_HOME`.

## Decisions

- **Two crates, one binary.** `crates/ur` becomes the library crate
  `crates/ox-server` (package `ox-server`) with `src/lib.rs` in place of
  `src/main.rs`. `crates/ox` depends on it and is the only binary. The
  client/server boundary stays compiler-enforced, and neither side's modules
  move or collide (both have an `acp` module).
- **The Ox client still launches its server as a child process.** The bundled
  server entry becomes `current_exe()` with args `["acp"]`, named `Ox`.
  `servers` and `--server` stay, so the client still works with other ACP
  servers and with `ox-fake-server`.
- **`ox acp` also accepts `auth login` and `auth logout`.** ACP terminal auth
  appends `auth login` to the server's configured command, so Zed runs
  `ox acp auth login`. The `acp` subcommand takes the same optional `auth`
  subcommand as the top level. `terminal_auth_method` keeps its args.
- **Each side reads only its own fields.** The client's `Config` drops
  `deny_unknown_fields` at the top level (it stays on `ServerConfig`); the
  server's `SettingsFile` already ignores unknown fields. Both already rewrite
  the file keeping fields they do not own. The client and server can both write
  the global settings file; the plan assumes the user never changes favorites
  and session settings at the same instant, so there is no locking.
- **The global settings file is `$HOME/.config/ox/settings.json`.** The client's
  `$XDG_CONFIG_HOME` lookup is deleted so one path function serves both sides:
  `settings::global_path(home)` in the server library, called by
  `crates/ox/src/config.rs`.
- **Every `ur` name becomes `ox`.** Workspace settings file `.ox/settings.json`,
  skills directory `~/.config/ox/skills`, database `ox.db` in `$OX_DATA_DIR`,
  else `$XDG_DATA_HOME/ox`, else `~/.local/share/ox`, keyring service `ox`,
  agent name `ox`, "You are Ox", the shell tool description, error hints
  (`run \`ox auth
  login\``), comments,
  test names, and test environment variables (`UR__`→`OX__`).
  The old database and keyring entry are left alone; the user logs in once more.
- **clap replaces the hand-written parser.** Effort parses through
  `EffortLevel::from_id` as a clap value parser whose error lists
  `EffortLevel::ALL`; a blank prompt is rejected by a value parser.
  `resolve_model` and `check_effort` stay in the server library because they
  need the model catalog.

## Naming

- **Ox** — the program, `ox`. Replaces the glossary's Ox and Ur entries, and
  "Ur" in comments that mean the program ("What Ox knows happened to a tool
  call").
- **Ox client** — the interactive terminal ACP client that `ox` runs.
- **Ox server** — the bundled ACP server that `ox acp` runs, also usable by
  other ACP clients. Crate `ox-server`.
- **Headless run** — one prompt run by `ox run`, as the glossary already uses.
- **Global settings file** — `~/.config/ox/settings.json`. Replaces "config
  file" in `crates/ox/src/config.rs` comments.
- **Workspace settings file** — `.ox/settings.json` in the workspace.

## Test plan

- `crates/ox/src/main.rs`: one table-driven test over `Args::try_parse_from`
  owns command parsing. Accepted: `ox`, `ox dir`, `ox --server X`,
  `ox run Hello` (default effort),
  `ox run --dir w Fix --model m --effort xhigh`, `ox acp`, `ox acp auth login`,
  `ox auth login`, `ox auth logout`. Rejected: `ox login`, `ox run`,
  `ox run ""`, `ox run --dir`, `ox run Hello again`,
  `ox run --effort huge Hello` (error says "not an effort level").
  `requires_an_existing_directory` stays.
- Delete `parses_auth_commands`, `parses_run_commands`, and
  `rejects_unknown_commands` from the old `crates/ur/src/main.rs`. Their
  `resolve_model` and `check_effort` assertions move to one test in
  `crates/ox-server/src/lib.rs`. Their workspace-model assertion is dropped;
  `workspace_settings_override_the_default_model` owns it.
- `crates/ox/src/config.rs`: `omitted_servers_mean_the_bundled_server...`
  expects a command ending in `ox` with args `["acp"]`, and gains a case with
  `model`, `effort`, and `mode` beside `favorites`, which the client accepts.
  `old_config_is_rejected_without_migration` stays (it tests `ServerConfig`).
- `crates/ox-server/src/settings.rs`:
  `saving_session_settings_preserves_other_fields...` writes `favorites` and
  `servers` into the global settings file first and asserts they survive a save.
  Paths in this and the other settings, skills, and ACP tests change to
  `.config/ox/...` and `.ox/...`.
- `crates/ox-server/src/system_prompt.rs`: expects "You are Ox".
- `crates/ox/tests/tui.rs`: both setups write `$HOME/.config/ox/settings.json`
  and stop setting `XDG_CONFIG_HOME`.
- Live check: `scripts/run.py` runs a prompt through `ox run`, and `ox` in a
  workspace starts the Ox server through `ox acp`.

## Implementation plan

1. `git mv crates/ur crates/ox-server`. In its `Cargo.toml`, rename the package
   to `ox-server`. In the root `Cargo.toml`, replace the `crates/ur` member and
   add `ox-server = { path = "crates/ox-server" }` to
   `[workspace.dependencies]`. Add `ox-server.workspace = true` to
   `crates/ox/Cargo.toml`.
2. Replace `crates/ox-server/src/main.rs` with `src/lib.rs`: the module list,
   `pub use sessions::EffortLevel`, and four entry functions built from the old
   `run` match arms: `serve()`, `run(dir, model, effort, prompt) -> answer`,
   `login()`, and `logout()`. Keep `absolute_dir`, `resolve_model`,
   `check_effort`, and `load_settings_and_catalog` private. Delete `USAGE`,
   `Command`, `command`, `run_command`, `usage_error`, and `print_help`.
3. In `crates/ox-server/src/settings.rs`, add
   `pub fn global_path(home: &Path) -> PathBuf` returning
   `home/.config/ox/settings.json`, use it in `load` and `save`, and change the
   workspace path to `.ox/settings.json`. Update the module comment.
4. Rename the remaining `ur` names listed under Decisions in `sessions.rs`,
   `auth.rs`, `skills.rs`, `acp.rs`, `tools/shell.rs`,
   `prompts/system_prompt.md`, and every comment and test in the crate that says
   Ur.
5. In `crates/ox/src/config.rs`, drop the top-level `deny_unknown_fields`, make
   `path()` call
   `ox_server::settings::global_path(ox_server::settings::home_dir()?)` (export
   `settings` or the two functions from the library), and make the bundled
   server `current_exe()` with args `["acp"]`, named `Ox`. Rename "config file"
   to "global settings file" in its comments.
6. In `crates/ox/src/main.rs`, add the `run`, `acp`, and `auth` subcommands with
   `args_conflicts_with_subcommands`, dispatch them to the library, and keep the
   no-subcommand path as the Ox client. Convert library errors to `anyhow` so
   every failure prints `ox: ...`. Write the parsing test.
7. Update `crates/ox/tests/tui.rs` for the new settings path.
8. Replace `examples/ox/settings.json` and `examples/ur/settings.json` with one
   `examples/settings.json` holding `model`, `effort`, `mode`, and `favorites`.
9. `Makefile`: build `-p ox` only, install only `ox`, drop `UR_CONFIG_DIR`, and
   copy `examples/settings.json` to `$(OX_CONFIG_DIR)/settings.json`.
10. `.github/workflows/release.yml`: build `-p ox` and package only `ox`.
11. `scripts/run.py`: run `cargo run -p ox -- run ...`; temporary workspace
    prefix `ox-`.

## Documentation updates

- `AGENTS.md`: the directory map (`crates/ox-server/`, `crates/ox/` as the
  command line and Ox client, the prompts path, "the example settings file") and
  the Backwards Compatibility paths (`ox.db`, `$OX_DATA_DIR`,
  `$XDG_DATA_HOME/ox`, `~/.local/share/ox`).
- `agents/architecture.md`: the opening paragraph (one program with the Ox
  server and the Ox client), "The client can launch the Ox server or another
  compatible ACP server", and "outside the Ox server".
- `agents/glossary.md`: replace Ox and Ur with Ox, Ox client, and Ox server;
  change "Ur" in Tool outcome and Model catalog to "Ox".
- `README.md`: install one binary, `ox auth login`, the Zed entry
  (`"command": ".../ox", "args": ["acp"]`), the settings file path without
  `$XDG_CONFIG_HOME`, and `ox run` and `ox acp` in place of `ur`.
