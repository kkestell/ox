# Select the model client from the model's provider

## Plan

`agents/plans/2026-10-01-003-select-model-client-by-provider.md`

## Summary

The plan's goal is met. Callers pass `model::Clients`, which selects the model
client from the model's provider. The caller lookups, the three guards, and the
`model::Client` enum are gone.

## Departures

- One compaction test passed the OpenAI fixture's client to `compact` directly.
  It now converts the fixture's client with `.into()`, because the fixture
  returns `openai::Client`.

## Decisions

- A model whose provider has no model client now panics in `model::Clients`. The
  guards it replaces returned an error for the same state.

## Automated checks

- `make format` passed.
- `make check` passed. That includes 204 `ox-server` tests and clippy.
- `make e2e` was not run, because terminal behavior did not change. `make check`
  skips its 12 tmux tests.

The complete test diff was inspected. Only call sites changed types. No
guarantee gained, lost, or moved its owning test.

## Manual verification

Live provider checks were not run.

## Follow-up

None.
