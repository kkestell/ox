# Workspace settings

## Goal

A workspace can override settings in `.ox/settings.json`. It uses the same
format as `~/.config/ox/settings.json`: a key it sets replaces the same key
from that file, and a key it leaves out keeps that file's value. A missing
workspace settings file changes nothing. Today the only key a workspace can
set is `model`:

```json
{ "model": "deepseek/deepseek-v4.1-flash" }
```

The model it names is the default model for new sessions in that workspace and
for `ox run` there without `--model`. `hooks` stays in
`~/.config/ox/settings.json` only.

Settings will grow. Adding a key later should take one field in the file
format, one in the effective settings, and one line where the workspace value
replaces the global one. The change should also add as little code as
possible, by giving both files one reader and removing the checks it replaces.

Use [architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), [testing guidance](../testing.md), and
[`AGENTS.md`](../../AGENTS.md) as the implementation constraints.

## Related code

- `src/settings.rs` — `load` and `load_from` read
  `~/.config/ox/settings.json`, reject a blank `model`, build the global hooks,
  suppress them inside hook commands, and return `Loaded` with the file's path
  for later error messages.
- `src/openrouter.rs` — `install_catalog` rejects a default model outside the
  catalog, then installs the catalog and default model. `default_model` reads
  it, and many tests use it as the model of the test catalog.
- `src/main.rs` — `load_settings_and_catalog` reads settings, fetches the
  catalog, and prefixes a catalog error with the settings path.
  `resolve_model` falls back to the default model for `ox run`.
- `src/acp.rs` — `ServerState::global_hooks`, used by prompt runs.
  `default_settings` supplies the default model to `new_session`,
  `load_session`, `set_config_option`, and the `/compact` handler as the
  fallback for a transcript with no model entry.
- `src/sessions.rs` — `saved_settings` uses that fallback only when the
  transcript is empty.

## Decisions

- Two types in `src/settings.rs`:
  - `SettingsFile` is the format both files share. Every key is optional and
    unknown keys are errors.
  - `Settings` is the effective settings for one workspace: `default_model`
    and `global_hooks`, with the model always present. It replaces `Loaded`.
- One reader, `read(path, catalog)`, parses either file and rejects a `model`
  outside the model catalog and invalid hooks, with every error prefixed by the
  file's path. A blank model is outside the catalog, so the separate blank
  check goes.
- `settings::load(catalog)` reads `~/.config/ox/settings.json` into `Settings`.
  That file must set `model`, because it supplies the default model for every
  workspace that does not set one.
- `Settings::for_workspace(&self, workspace_path)` reads the workspace settings
  file and returns a copy of `self` with each key the file sets replaced. A
  missing file returns the copy unchanged. Any other error, including a
  directory in its place, is returned.
- Setting `hooks` in the workspace settings file is an error naming the file.
  Hooks run commands without any other approval, so a repository cannot add
  them, and the error keeps them from being ignored without notice.
- `ServerState` holds `Settings` in place of `global_hooks`. The default model
  moves out of the model catalog: `install_catalog` takes only the models, and
  `openrouter::default_model` goes. Tests use
  `openrouter::fixture::DEFAULT_MODEL`, the model the test catalog already
  installs as its default.
- Startup fetches the model catalog before reading
  `~/.config/ox/settings.json`, so both files share the catalog check.
  `load_settings_and_catalog` no longer adds the settings path to errors. As a
  result, a settings error is reported only after the catalog download
  succeeds.
- The workspace settings file is read when a session becomes active, together
  with `AGENTS.md` and the skill catalog, and once by `ox run`. An invalid file
  fails activation or the run with its path, like `AGENTS.md`. Only a session
  with an empty transcript uses the default model. A session with turns keeps
  the model in its model entry.
- `new_session` and `load_session` take the default model from
  `for_workspace`. `set_config_option` and `/compact` pass the active
  session's ACP selections as the fallback instead, because for an empty
  transcript those selections already hold the default model captured at
  activation. `default_settings` goes.

## Naming

- **Settings file** — Unchanged: `~/.config/ox/settings.json`, read once at
  process startup.
- **Workspace settings file** — `.ox/settings.json` in a session workspace. It
  uses the settings file format and may set every key except `hooks`. The name
  is used in the glossary, the architecture document, the README, and doc
  comments in `src/settings.rs`.
- **Default model** — Change the glossary definition to: the model named by
  `model` in the workspace settings file, else by `model` in the settings
  file, used for a new session and for `ox run` without `--model`. It is
  `Settings::default_model` in code.
- `SettingsFile` and `Settings` — The file format and the effective settings,
  as described in Decisions. They appear only in `src/settings.rs` and its
  callers.

## Test plan

- `src/settings.rs`: update
  `loads_settings_and_suppresses_global_hooks_inside_hooks` to use catalog
  models instead of `a/b`. Add a model outside the catalog as an invalid case.
  The blank model and missing model cases stay invalid.
- `src/settings.rs`: add `workspace_settings_override_the_settings_file`:
  - A missing file and `{}` keep the default model and global hooks.
  - A catalog model replaces the default model and keeps the global hooks.
  - A file with `hooks`, a model outside the catalog, an unknown key, and
    malformed JSON are errors naming `.ox/settings.json`.
- `src/acp.rs`: add `new_sessions_use_the_workspace_default_model`. A new
  session in a workspace whose settings file names another catalog model offers
  that model in its configuration options. After a later process loads the
  session with an empty transcript, the model is the same. An invalid workspace
  settings file fails activation with its path.
- `src/main.rs`: `parses_run_commands` resolves an absent `--model` to the
  workspace model for a workspace with a settings file, and to the default
  model without one.
- Every other test keeps its assertions and replaces
  `openrouter::default_model()` with `openrouter::fixture::DEFAULT_MODEL`.

## Implementation plan

1. `src/openrouter.rs`: `install_catalog` takes only the models and no longer
   checks a default model. Remove `default_model` and the `Catalog` field, and
   add `fixture::DEFAULT_MODEL`.
2. `src/settings.rs`: add `SettingsFile` and `Settings`. Replace `load_from`
   with `read(path, catalog)`. `load(catalog)` builds `Settings` from the
   settings file and keeps hook suppression inside hook commands. Add
   `Settings::for_workspace`. Update the module doc comment.
3. `src/main.rs`: `load_settings_and_catalog` fetches the catalog, calls
   `settings::load` with it, installs the catalog, and returns `Settings`.
   `serve_stdio` takes `Settings`. The `Run` branch resolves the workspace path
   first, calls `for_workspace`, and passes its default model to
   `resolve_model` and its global hooks to `run_headless`.
4. `src/acp.rs`: replace `ServerState::global_hooks` with `settings`. Use
   `for_workspace` in `new_session` and `load_session`, and the active
   session's ACP selections as the fallback in `set_config_option` and
   `/compact`. Remove `default_settings`.
5. Replace `openrouter::default_model()` in tests, add the tests above, and
   compare the line counts of the changed files before and after with `rsloc`.

## Documentation updates

- `eng/architecture.md`: the Settings section describes the workspace settings
  file, how its keys replace the settings file's, that `hooks` is not allowed
  there, when it is read, and that the catalog is fetched before the settings
  file is read.
- `eng/glossary.md`: the Default model definition and the new Workspace
  settings file term.
- `README.md`: the Settings section describes `.ox/settings.json`.
- `AGENTS.md`: the `src/settings.rs` entry describes the workspace settings
  file, and the `src/openrouter.rs` entry no longer lists the default model.
