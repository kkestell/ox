# Select the model client from the model's provider review

## Scope and coverage

Reviewed commit `3ff4dbb` against
`agents/plans/2026-10-01-003-select-model-client-by-provider.md`: the changes in
`model.rs`, `acp.rs`, `acp/prompt.rs`, `compaction.rs`, `subagents.rs`,
`lib.rs`, and `openai.rs`, and the code that builds `model::Clients` and the
model catalog (`lib.rs` `discover_catalogs` and `load_settings_and_catalog`,
`model::install_catalog`, and `ModelRequestParameters::new`). Lenses:
correctness, error-handling, simplicity, and documentation. Live provider
requests were not made.

## Findings

No confirmed findings. The panic in `model::Clients` cannot be reached in normal
use: the installed catalog holds only models from providers whose model client
was built, every model request takes its model from `ModelRequestParameters`,
which rejects models outside the catalog, and compaction summarizes with the
session's own model.

## Checks run

- `make check` passed, including 204 `ox-server` tests and clippy. It ignores
  the 12 tmux tests.
- `make e2e` was not run, because terminal behavior did not change.

## Verdict

The change meets its plan and deletes the caller lookups, the three guards, and
the `model::Client` enum without changing behavior. No action needed.
