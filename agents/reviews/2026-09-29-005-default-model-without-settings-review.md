# Review of starting Ur without a settings file

## Scope and coverage

Commit `28750ad`: `crates/ur/src/settings.rs` and its work log, against the plan
`agents/plans/2026-09-29-004-default-model-without-settings.md`. Also read the
caller `load_settings_and_catalog` and `ur run` in `crates/ur/src/main.rs`, the
catalog filter in `crates/ur/src/openrouter.rs`, `README.md`, and
`examples/ur/settings.json`. Lenses: correctness, error-handling, simplicity,
testing, and documentation. The live check against OpenRouter was not repeated.

## Findings

No open findings.

### Fixed

- **Three places check that the model is in the catalog**
  (`crates/ur/src/settings.rs:55`): `read` rejected a file's model outside the
  catalog, `load_from` added a match arm with its own error for the built-in
  model, and `validate` looked the model up again behind an `expect` that relied
  on the other two. The check now lives only in `validate`, which returns
  `model <id> is not in the OpenRouter model catalog` naming the file, and
  `load_from` uses `unwrap_or_else` for the built-in model. `read` no longer
  takes the catalog. The test for a catalog without the built-in model now
  asserts the full message. Production lines in `settings.rs` fell from 175 to
  161 (`rsloc`).

## Checks run

- `make check` — passed.

## Verdict

The change meets its plan. One simplification was fixed, and nothing is left
open.
