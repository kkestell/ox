# Configured model catalog

## Goal

Move the model catalog from `MODEL_CATALOG` in `src/openrouter.rs` to
`~/.config/ox/settings.json`. Ox has no built-in models. Starting the ACP server
or the headless entry point fails, naming the file, when the settings file is
missing or declares no models. `ox auth` and `--help` do not read settings.

Follow [architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), and [testing](../testing.md).

## Related code

- `src/openrouter.rs`: `CatalogModel`, `MODEL_CATALOG`, `DEFAULT_MODEL`,
  `catalog_model`, and the request body builders that read effort mappings.
- `src/settings.rs`: the settings loader, which reads global hooks and
  suppresses them under `OX_IN_HOOK`.
- `src/main.rs`: command parsing, which validates `--model` against the catalog,
  and process entry.
- `src/acp.rs`: `default_settings`, `validate_settings`, `config_options`,
  `serve_stdio`, and `run_headless`, which each call `settings::load`.
- `src/compaction.rs`, `src/acp/convert.rs`, `src/acp/prompt.rs`: context limit
  and catalog checks through `catalog_model`.
- `examples/skills/goal/scripts/judge.py`: a nested `ox run --model` inside a
  hook command.

## Decisions

### Settings

Add a required `models` list next to the optional `hooks` object:

```json
{
  "models": [
    {
      "id": "deepseek/deepseek-v4.1-flash",
      "name": "DeepSeek V4.1 Flash",
      "context_limit": 1048576,
      "effort_mapping": { "low": "low", "medium": "high", "high": "max" }
    }
  ],
  "hooks": { "after_run": { "command": "python3 scripts/report.py" } }
}
```

The first model is the default for new sessions and for `ox run` without
`--model`. The loader rejects any of the following, with an error naming the
file, as it does for invalid hooks:

- a missing file
- a missing or empty `models` list
- unknown fields
- a blank `id`, `name`, or effort mapping value
- duplicate ids
- a `context_limit` of 8,000 or less, which would underflow
  `compaction::budget`

`OX_IN_HOOK` suppresses only global hooks. Models always load, because a hook's
nested `ox run`, such as the goal skill's judge, needs the model catalog.

### Process-wide catalog

Keep the model catalog in a `static CATALOG: OnceLock<Vec<CatalogModel>>` in
`src/openrouter.rs`. The process installs it once at startup. `catalog_model`
keeps its signature, so compaction, usage updates, prompt runs, and request body
builders do not change. Installing twice, or reading before installing, is a
bug and panics.

Under `#[cfg(test)]`, `catalog()` returns a fixed catalog of the three current
models, so tests never read or install settings.

### Command parsing

`--model` is kept as an unvalidated `Option<String>` during parsing. After
settings load, `run()` resolves it against the model catalog using the existing
"choose one of" error, or uses the default model. The loaded global hooks are
passed into `acp::serve_stdio` and `acp::run_headless` instead of each loading
settings again.

## Naming

- **Model catalog**: now the models declared in the settings file, each with its
  effort mapping and context limit. `catalog()` in code.
- **Default model**: the first model in the model catalog. `default_model()` in
  code, replacing `DEFAULT_MODEL`.
- `effort_mapping`: the settings key for a model's effort mapping, with `low`,
  `medium`, and `high` entries. It replaces the `openrouter_efforts` field.

## Test plan

- Extend `settings::tests::loads_global_hooks_and_suppresses_them_inside_hooks`
  rather than adding a test:
  - A missing file is an error.
  - Add table rows for a missing or empty `models` list, a duplicate id, a small
    `context_limit`, an unknown model field, a blank effort mapping value, and a
    valid model catalog with hooks.
  - Under `OX_IN_HOOK`, models load and global hooks are dropped.
- Update `main::tests::parses_run_commands` for an unvalidated optional model.
- Add `models` to the settings file written by
  `acp::tests::headless_signals_clean_up_and_save_even_when_repeated`.
- Replace `MODEL_CATALOG[n]` and `DEFAULT_MODEL` in existing tests with
  `catalog()[n]` and `default_model()`. Do not add test functions.

## Implementation plan

1. In `src/openrouter.rs`:
   - Give `CatalogModel` owned `String` fields, `Deserialize`, and an
     `EffortMapping` struct for `effort_mapping`.
   - Make `openrouter_effort` return `Option<&str>`.
   - Replace `MODEL_CATALOG` and `DEFAULT_MODEL` with `CATALOG`,
     `install_catalog`, `catalog`, and `default_model`, plus the test catalog.
2. In `src/settings.rs`:
   - Add `models` to `Settings` with its validation.
   - Return the model catalog and optional global hooks.
   - Treat a missing file as an error.
   - Apply `OX_IN_HOOK` only to hooks.
3. In `src/main.rs`:
   - Parse `--model` as `Option<String>`.
   - For serve and run, load settings and install the catalog, then resolve the
     model.
   - Pass global hooks to `acp::serve_stdio` and `acp::run_headless`.
4. In `src/acp.rs`:
   - Accept global hooks in `serve_stdio` and `run_headless`.
   - Use `catalog()` and `default_model()` in `default_settings` and
     `config_options`.
   - Adjust `String` borrows where needed.
5. Update the test references listed in the test plan.

## Documentation updates

- `README.md`, `AGENTS.md` (`src/settings.rs` and `src/openrouter.rs` entries),
  and `eng/architecture.md` (Global hooks section, retitled for settings):
  document the `models` list, the required file, and the default model.
- `eng/glossary.md`: redefine **Model catalog** and add **Default model**.
- `examples/skills/careful/README.md`: drop "This file configures hooks only".
