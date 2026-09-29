# Start Ur without a settings file

## Goal

Someone who copies the `ox` and `ur` binaries into `~/.local/bin` and runs
`ur auth login` can use Ox with no settings file. When
`~/.config/ur/settings.json` is missing or has no `model`, Ur uses a built-in
default model. Ur writes no file at startup; the existing save on a change of
model, effort level, or session mode still creates the file. Ox already starts
without its settings file and needs no change.

## Related code

- `crates/ur/src/settings.rs` — `load_from` reads the global settings file and
  rejects a missing file or a missing `model`. `for_workspace` already treats a
  missing file as no change. `save` already creates the global file.
- `crates/ur/src/openrouter.rs` — `parse_catalog` and its `fixture` module. Test
  code indexes the fixture catalog by position, so this plan does not add a
  model to it.
- `crates/ur/src/main.rs` — `load_settings_and_catalog` runs the load for both
  the server and `ur run`, so one change covers both.

## Decisions

- The built-in default model is `~deepseek/deepseek-flash-latest`. It is an
  OpenRouter alias for the latest DeepSeek Flash model, so the six-month release
  filter in the model catalog is unlikely to drop it, unlike a fixed model ID.
  On 2026-09-29 it is in OpenRouter's catalog, passes the filter, and lists
  efforts `low`, `high`, and `max`. The saved session settings hold the alias,
  not the model it resolves to.
- The built-in default lives in code, not in a file Ur writes at startup, so a
  fresh install changes nothing on disk until the user picks something.
- When the built-in default is not in the model catalog and no `model` is set,
  Ur exits with an error that names the `model` setting and the file. It does
  not choose another model. The global file loads before any workspace is known,
  so a workspace `model` does not avoid this error.

## Naming

- `built-in default model` — the model used when no settings file sets `model`.
  In code it is `BUILT_IN_MODEL` in `settings.rs`. It is not `DEFAULT_MODEL`,
  which is the test fixture's model.

## Test plan

- One table-driven test in `settings.rs` owns loading the global file. Its cases
  and expected results:
  - A missing file and `{}` give `BUILT_IN_MODEL`, effort `default`, mode `ask`.
  - `{"model":"M"}` gives `M`, and unknown keys are still ignored.
  - An unknown model, a blank model, empty text, and invalid JSON are errors
    that name the file.
  - A directory in place of the file is an error.
  - A catalog without `BUILT_IN_MODEL` and a file with no `model` is an error
    that names `model` and the file.
- The cases that need `BUILT_IN_MODEL` in the catalog use a one-model catalog
  parsed in the test with `parse_catalog`. The shared fixture catalog stays as
  it is.
- Effort and mode validation tests are unchanged.

## Implementation plan

1. In `crates/ur/src/settings.rs`, add `BUILT_IN_MODEL` with a comment saying it
   is an OpenRouter alias. Derive `Default` for `SettingsFile`. In `load_from`,
   treat a missing file as an empty `SettingsFile`, as `for_workspace` does, and
   use `BUILT_IN_MODEL` when `model` is absent. When it is used and the catalog
   lacks it, return an `invalid` error that names the `model` setting. Update
   the doc comments on the module and on `load`, which say the file must name
   the model.
2. Replace `settings_require_a_known_default_model` with the table-driven test
   above, keeping its invalid-input cases.
3. Run `make check`.
4. Live check, which needs the OpenRouter key in `.env` and costs a few cents:
   run the built `ur run` with a temporary `HOME`, `UR_DATA_DIR`, and
   `OPENROUTER_API_KEY`. Confirm the model request succeeds with the alias ID
   and that no settings file exists afterward.
