# Multi-provider models

## Goal

Let one Ox server use models from every model provider whose credentials are
available when the process starts. Remove model provider selection from
settings, identify every Ox model with a provider-prefixed value such as
`openrouter:deepseek/deepseek-v4.1-flash`, and show the model provider in the Ox
client's model picker.

Follow `AGENTS.md`, `agents/architecture.md`, `agents/code-style.md`,
`agents/glossary.md`, and `agents/testing.md`.

## Related code

- `crates/ox-server/src/model.rs` — owns model providers, catalog models, the
  installed model catalog, and the concrete model clients.
- `crates/ox-server/src/lib.rs`, `auth.rs`, and `openai_auth.rs` — discover
  credentials, load the startup model catalog, construct model clients, and run
  headless prompts.
- `crates/ox-server/src/settings.rs` — reads defaults and OpenRouter provider
  pins, validates model choices, and saves session settings.
- `crates/ox-server/src/acp.rs` and `acp/prompt.rs` — advertise model choices,
  select a model client for a prompt run or compaction, change models between
  turns, and save the captured model in each turn start.
- `crates/ox-server/src/openrouter.rs` and `openai.rs` — keep provider-native
  model IDs at the HTTP boundary and project transcript continuation metadata
  into provider-specific requests.
- `crates/ox/src/tui.rs` — reads model choice metadata and lays out the model
  picker.

## Decisions

- A qualified model ID is the model provider ID, one colon, and the provider's
  model ID. Qualified model IDs are used by `model`, `favorites`, keys under
  `models`, `--model`, ACP model choice values, session settings, and turn
  starts. `CatalogModel::id` remains the provider-native model ID used in HTTP
  request bodies; catalog lookup and a catalog model method translate between
  the two forms.
- Startup checks both providers for credentials. `OPENROUTER_API_KEY` or a saved
  OpenRouter key enables OpenRouter; a saved OpenAI credential record enables
  OpenAI. Ox fetches and concatenates the enabled catalogs in the fixed order
  OpenRouter then OpenAI and retains one client for each. A credential or
  catalog error for an enabled provider stops startup rather than silently
  omitting that provider. With no enabled provider, startup fails and names both
  login commands.
- Without a configured model, Ox uses
  `openrouter:~deepseek/deepseek-flash-latest` when OpenRouter is enabled. When
  only OpenAI is enabled, it uses the first usable OpenAI catalog model.
- The `provider` settings key is rejected with a message directing the user to a
  qualified model ID. OpenRouter provider pins remain global-only; their
  `models` keys become qualified model IDs and must identify an OpenRouter
  catalog model.
- The installed model catalog contains only models from enabled providers. ACP
  advertises the complete installed catalog for every session, so changing the
  model can also change the model provider for the next turn. A prompt run, its
  subagents, and its compaction requests continue to share the captured model
  and its one concrete model client.
- Provider continuation metadata is reused only when the assistant batch came
  from the same model provider as the new request. The provider is recovered
  from the qualified model ID in the batch's turn start. Switching providers
  still projects the shared messages and tool outcomes but does not send opaque
  metadata to the wrong provider.
- The ACP model choice `_meta` gains `provider`, containing the display name
  `OpenRouter` or `OpenAI`. The Ox client treats it as optional for
  compatibility with other ACP servers and draws it between the model name and
  the existing price columns, leaving the column blank when metadata is absent
  or invalid.
- Catalogs and model clients are fixed for the process lifetime. CLI login and
  logout remain explicit per provider, and a running server must be restarted to
  reflect credential changes. Remove ACP's unqualified terminal-login and logout
  capabilities rather than making them modify a live catalog.

## Naming

- **Model provider** — OpenRouter or OpenAI. It retains the glossary term but is
  selected by each qualified model ID rather than once per Ox server process.
- **Model catalog** — the models from every model provider enabled at process
  startup.
- **Model client** — one provider's HTTP client. The process can retain one for
  each enabled model provider; a prompt run uses the client for its captured
  model.
- **Qualified model ID** — `<model-provider-id>:<provider-model-id>`, used
  everywhere outside provider-specific catalog parsing and HTTP request bodies.

## Test plan

- Model and settings tests cover qualified lookup for both providers, rejection
  of missing or unknown prefixes, provider selection by equal provider-native
  IDs, the OpenRouter-first and OpenAI-only defaults, rejection of `provider`,
  qualified OpenRouter provider pins, and settings save/reload.
- Startup tests with scripted provider services cover OpenRouter-only,
  OpenAI-only, and combined catalogs and clients; no credentials; and failure to
  load one enabled provider without falling back to the other.
- ACP tests check that model choices include every installed provider, use
  qualified values, and carry provider metadata. An orchestration test changes a
  session from an OpenRouter model to an OpenAI model and verifies that the next
  prompt and manual compaction use the OpenAI fixture while earlier turns remain
  replayable.
- Provider request tests switch a transcript between providers and verify that
  each request keeps ordinary transcript content while omitting continuation
  metadata created by the other provider. Session-store tests reject a turn
  start whose model has no known provider prefix.
- TUI unit tests check the provider column with both providers and with missing
  metadata, including clipping and alignment. The model picker end-to-end test
  checks the OpenRouter column and qualified model and favorite values written
  to settings.
- Run the standard code checks and the terminal end-to-end suite. Perform one
  live headless request with each authenticated provider and one live terminal
  session that changes providers between turns; report either live check that
  cannot run because its credentials are unavailable.

## Implementation plan

1. In `model.rs`, add qualified model ID formatting and parsing, make catalog
   lookup use both the prefix and provider-native ID, install one combined
   catalog, and add a concrete collection that returns the retained model client
   for a model provider. Keep provider-native IDs in request bodies and update
   model fixtures to distinguish provider-native and qualified IDs.
2. In `openai_auth.rs` and `lib.rs`, detect saved OpenAI credentials, discover
   both providers at startup, fetch every enabled catalog, build the client
   collection, enforce the no-credentials error and catalog order, and select
   the resolved model's client for headless runs.
3. In `settings.rs`, remove process-wide model provider selection from
   `Settings`, reject the obsolete settings key, validate qualified global and
   workspace model values against the combined catalog, implement the agreed
   default rule, and apply provider pins through qualified OpenRouter model IDs.
4. In `acp.rs`, replace the process-wide provider and optional client cache with
   the startup client collection. Advertise every installed catalog model with a
   qualified value and provider metadata, choose a client from the active
   session's model for prompts and compaction, allow loading and changing models
   across providers, and remove ACP terminal authentication and logout
   advertising and handlers.
5. In `acp/prompt.rs`, save the catalog model's qualified ID in each turn start
   while continuing to use its provider-native ID for requests. In
   `sessions.rs`, enforce the qualified model invariant when validating
   transcripts. Update the affected prompt, compaction, conversion, session, and
   subagent fixtures and tests in `acp/prompt.rs`, `acp/convert.rs`,
   `compaction.rs`, `sessions.rs`, and `subagents.rs`.
6. In `openrouter.rs` and `openai.rs`, use each assistant batch's turn-start
   model provider when deciding whether to project its continuation metadata.
   Add cross-provider projection cases while preserving each provider's existing
   request format and provider-native model field.
7. In `crates/ox/src/tui.rs`, read the optional provider metadata into
   `ModelChoice` and add the aligned provider column to model picker rows.
   Update its render tests and `crates/ox/tests/tui.rs` for the new column and
   qualified settings values.
8. Correct the affected documentation and example settings, then run the test
   plan.

## Documentation updates

- `README.md` — replace process-wide provider selection with startup credential
  discovery, qualified model IDs and defaults, multi-provider model choice, and
  the restart requirement after credential changes.
- `examples/settings.json` — remove `provider` and qualify the model, favorites,
  and OpenRouter provider-pin key.
- `agents/architecture.md` — correct the model-client boundary and remove the
  one-model-provider-per-process constraint while retaining one provider per
  captured turn.
- `agents/glossary.md` — update model provider, model client, and model catalog,
  and add qualified model ID.
- `agents/testing.md` — replace the selected-provider live-check instructions
  with qualified model IDs and per-provider credentials.
