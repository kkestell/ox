# Multi-provider models review

## Scope and coverage

Reviewed the uncommitted implementation of
`agents/plans/2026-10-01-002-multi-provider-models.md`: startup credential and
catalog discovery, qualified model IDs in settings, sessions, and ACP, per-turn
client selection, continuation-metadata projection across providers, compaction
suffix provenance, the model picker's provider column, and the documentation the
plan names. Lenses: correctness, simplicity, architecture, testing, and
documentation.

Live provider checks were not run, by the work log or by this review; they need
authenticated provider accounts.

## Fixed

- **Qualified model IDs were parsed in three places**
  (`crates/ox-server/src/settings.rs:104`,
  `crates/ox-server/src/settings.rs:160`): settings validation and provider pins
  each reimplemented the split and catalog lookup that `model.rs` already had.
  Added `Provider::split_qualified_model_id`
  (`crates/ox-server/src/model.rs:44`); catalog lookup, default-model
  validation, and pins now use it.
- **A test-only client override bypassed provider routing**
  (`crates/ox-server/src/acp.rs:216`): `ServerState` carried a `#[cfg(test)]`
  mutex whose client was returned for every provider, so ACP tests could not
  catch a model routed to the wrong client, and one assertion ("empty command
  needs no client") checked a field production code never set. Removed the
  override; tests build the state with fixture `Clients`.
- **`initialize_response` kept an unused request parameter**
  (`crates/ox-server/src/acp.rs:767`): left over from terminal-login
  advertising. Removed the parameter and the test setup that built it.
- **Compaction duplicated the provider transcript dispatch**
  (`crates/ox-server/src/model.rs:82`): `compaction::transcript_from` repeated
  `Provider::transcript` with an extra turn-provider argument, and the provider
  modules kept one-line wrappers for the old signature. `Provider::transcript`,
  `openrouter::chat_messages`, and `openai::input` now take the turn provider
  directly; `transcript_from` and both wrappers are gone.

## Findings

### Medium

#### Architecture

- **OX-0028 Callers pair a model with a client by hand, and three runtime guards
  check that they match** (`crates/ox-server/src/acp/prompt.rs:338`,
  `crates/ox-server/src/model.rs:163`, `crates/ox-server/src/model.rs:184`): ACP
  prompts (`acp.rs:690`), manual compaction (`acp.rs:245`), headless runs
  (`lib.rs:132`), and the ACP test helper each look up the model's provider,
  fetch that provider's client with an `expect`, and pass the model and the
  client separately to `prompt::run`. `AgentTurn::open`,
  `Client::stream_completion`, and `Client::summarize` then check that the two
  agree. With clients now selected from the model's own provider, those checks
  guard a state no caller can produce, and no test reaches them. Suggested fix:
  pass `model::Clients` to `prompt::run` and `compaction::compact` and select
  the client from `parameters.model.provider` in one place, deleting the four
  caller lookups and the three guards. This changes the `prompt::run` signature
  used across the test suite, so it needs a plan.

## Checks run

- `cargo test -p ox-server` — passed: 204 tests.
- `make format` — passed.
- `make check` — passed.

`make e2e` was not run; the fixes do not change terminal behavior.

## Verdict

The change meets its plan. Four simplifications were fixed in place; one medium
architecture finding is left open for a plan.
