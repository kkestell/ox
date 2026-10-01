# Select the model client from the model's provider

## Goal

Fix OX-0028. Every caller of `prompt::run` and `compaction::compact` looks up
the model's provider, fetches that provider's model client with an `expect`, and
passes the model and the model client separately. Three runtime guards then
check that the two agree, a state no caller can produce. When this work is done,
callers pass `model::Clients`, the model client is selected from the model's
provider in `model::Clients` alone, the caller lookups and the three guards are
gone, and the `model::Client` enum no longer exists.

## Related code

- `crates/ox-server/src/model.rs:127` — the `model::Client` enum, its
  `From<openrouter::Client>`, `Clients::client`, `Client::provider`, and the
  guards in `Client::stream_completion` (`:163`) and `Client::summarize`
  (`:184`).
- `crates/ox-server/src/acp/prompt.rs:204` — `prompt::run` takes
  `impl Into<model::Client>`. `AgentTurn::open` holds the guard (`:338`) and
  passes the model client to `subagents::Launch`. `request_completion` (`:487`)
  and `compact` (`:532`) use it, and the stall retry checks
  `self.model_client.provider()` (`:439`).
- `crates/ox-server/src/compaction.rs:468` — `compact` takes `&Client` and calls
  `client.summarize`.
- `crates/ox-server/src/subagents.rs:37` — `Launch::model_client`, passed to
  each subagent's `prompt::run`.
- `crates/ox-server/src/acp.rs` — `ServerState::client` (`:216`), the lookups in
  `compact_session` (`:245`) and `spawn_prompt_run` (`:690`),
  `run_headless`/`run_headless_prompt` (`:808`), the test helper `clients`
  (`:1064`), and `run_selected_prompt` (`:1293`).
- `crates/ox-server/src/lib.rs:125` — `run` looks up the provider and model
  client before calling `acp::run_headless`.
- `crates/ox-server/src/openai.rs:766` — the OpenAI test fixture's `client`
  wraps its HTTP client in `model::Client::OpenAI`, so tests destructure it with
  `let ... else` (`lib.rs:224`, `:242`, `:285`, `:309`, `acp.rs:1209`).

## Decisions

- `model::Clients` dispatches model requests itself. It gains
  `stream_completion(parameters, input)` and
  `summarize(model, previous, piece)`, which match on
  `parameters.model.provider` and `model.provider` and call that provider's
  model client. A missing model client panics with "every installed model
  provider has a client": the model catalog only holds models from providers
  whose model client was built, so a miss is a bug. This replaces the
  `model::Client` enum, `Clients::client`, `Client::provider`, and the three
  guards.
- `prompt::run` takes `impl Into<model::Clients>`, and `compaction::compact`
  takes `&model::Clients`. `From<openrouter::Client>` and `From<openai::Client>`
  for `model::Clients` fill only that provider, so tests keep passing
  `server.client()` and `&server.client().into()` unchanged.
- `AgentTurn` and `subagents::Launch` hold `clients: model::Clients` in place of
  `model_client`. The stall retry checks `self.parameters.model.provider`.
- `CompletionStream` stays as it is.

## Naming

- `model::Clients` — the model clients for every model provider with
  credentials. Fields and parameters holding it are named `clients`.
- Model client — `openrouter::Client` or `openai::Client`, as in
  `agents/glossary.md`.

## Test plan

- No guarantee changes, so no test is added or removed. The existing prompt run,
  subagent, compaction, ACP, and headless tests cover selecting the model client
  for OpenRouter and OpenAI models, including `acp.rs` tests that switch
  providers between turns.
- Update test call sites only where the types change: the OpenAI fixture's
  `client`, the `let ... else` destructuring, the `acp::tests::clients` helper,
  and the `clients.client(...).is_some()` assertions in `lib.rs` tests, which
  check the `openrouter` and `openai` fields instead.

## Implementation plan

1. In `model.rs`, delete the `model::Client` enum, its `From`,
   `Clients::client`, and `impl Client`. Add `Clients::stream_completion` and
   `Clients::summarize`, and `From<openrouter::Client>` and
   `From<openai::Client>` for `Clients`.
2. In `compaction.rs`, change `compact` to take `clients: &model::Clients` and
   call `clients.summarize`.
3. In `acp/prompt.rs`, change `prompt::run` and `AgentTurn::open` to take
   `model::Clients`, delete the guard, rename the `AgentTurn` field to
   `clients`, and make the stall retry check `self.parameters.model.provider`.
4. In `subagents.rs`, rename `Launch::model_client` to
   `clients:
   model::Clients`.
5. In `acp.rs`, delete `ServerState::client`. Pass `self.clients.clone()` to
   `prompt::run` in `spawn_prompt_run` and `&self.clients` to
   `compaction::compact` in `compact_session`, deleting their lookups. Change
   `run_headless` to take `clients: model::Clients` and `run_headless_prompt` to
   take `impl Into<model::Clients>`. Delete the test helper `clients` and the
   provider lookup in `run_selected_prompt`, passing `server.client().into()`
   and `state.clients.clone()` instead.
6. In `lib.rs`, make `run` pass `clients` to `acp::run_headless`, deleting the
   provider and model client lookup.
7. Make the OpenAI test fixture's `client` return `openai::Client`, and replace
   the `let ... else` destructuring and the `model::Client::OpenAI(...)` wrapper
   at `acp/prompt.rs:821` with the plain value.
