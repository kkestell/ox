# OpenAI subscription support

## Goal

Run Ox's existing prompt run, tools, subagents, and compaction using an eligible
ChatGPT subscription. Authenticate explicitly with `ox auth login openai` or
`ox auth login openrouter`. OpenAI authentication must complete before the Ox
server starts.

Follow `AGENTS.md`, `agents/architecture.md`, `agents/code-style.md`,
`agents/glossary.md`, and `agents/testing.md`.

## Related code

- `crates/ox/src/main.rs` — authentication commands and their ACP aliases.
- `crates/ox-server/src/auth.rs` and `lib.rs` — OpenRouter credentials, login,
  and startup catalog loading.
- `crates/ox-server/src/settings.rs` — settings validation and provider pins.
- `crates/ox-server/src/openrouter.rs` — catalog types, request parameters,
  transcript encoding, request bodies, completion types, and streamed output.
- `crates/ox-server/src/compaction.rs` — projects the transcript into request
  messages and measures request bodies, including summarizer requests.
- `crates/ox-server/src/acp.rs`, `acp/prompt.rs`, `acp/convert.rs`, and
  `subagents.rs` — own and share the OpenRouter client and expose its catalog
  and completions through ACP.
- `crates/ox-server/src/sessions.rs` — continuation metadata, token usage,
  session settings, and session cost.
- `crates/ox/src/tui.rs` — already accepts absent model prices and session cost.

## Decisions

- **One provider per server process.** Add a global `provider` setting with
  values `openrouter` and `openai`, defaulting to `openrouter`. Workspace files
  cannot set it. A concrete enum dispatches to the two HTTP clients. Install
  only the selected provider's model catalog. Main sessions, child sessions, and
  summarizer requests use that provider.
- **Explicit authentication commands.** Require a provider argument on both
  `login` and `logout`, including `ox acp auth` aliases. Commands without the
  argument fail with usage instructions. Login authenticates the named provider
  without changing settings or loading the model catalog. ACP logout removes
  credentials for the process's selected provider and clears its cached HTTP
  client. OpenRouter terminal authentication appends `auth login openrouter`.
- **OpenAI login precedes startup.** OpenAI startup obtains a valid access token
  and fetches its authenticated model catalog before validating settings.
  Missing credentials stop startup with `run ox auth login openai`. OpenRouter
  retains its public catalog and existing terminal authentication lifecycle.
  After an OpenAI logout, log in again and restart the server to refresh its
  catalog.
- **One saved ChatGPT account.** Ox owns one protected credential record and a
  stable installation host ID. Reauthorization reuses the issued client ID and
  checks the verified account identity. To replace the account, explicitly log
  out first. Keep the registration and host ID through logout; remove access,
  refresh, and retained ID tokens. Do not read another application's
  credentials.
- **Use the documented public subscription route.** The
  [registration guide](https://developers.openai.com/siwc/token-sharing-open-source/sign-in)
  specifies dynamic registration, a loopback callback, PKCE, ID-token
  validation, and the `chatgpt.tokens.use.direct` permission. Use the issued
  client ID for code exchange and refresh, never `dynamic_agent_client`. Store
  the granted scopes and expiry with the tokens.
- **Refresh belongs to OpenAI authentication.** Read the latest credentials
  before obtaining an access token. Serialize token replacement across Ox
  processes with one credential-file lock, rechecking expiry after acquiring it.
  This lock covers only credential refresh, not model requests or session state.
  Atomically save rotating refresh tokens with the replacement access token.
  Authentication failure requires another explicit login; do not retry model
  requests automatically. See
  [refreshing tokens](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions#refreshing-tokens).
- **Keep provider formats at the HTTP boundary.** Move shared catalog and
  completion types into `model.rs`. Each provider encodes transcript entries
  directly into its own input format. Compaction chooses the transcript prefix
  and summary; it calls provider-specific encoders and body builders for
  projection and size estimates. There is no intermediate conversion from
  OpenRouter messages to OpenAI input.
- **Use the account's catalog.** OpenAI uses returned slugs as model IDs and
  preserves the server's display order. Map the catalog's context and capability
  metadata into the shared catalog type; verify its actual response shape before
  defining the parser and fixtures. Do not apply OpenRouter's release-date or
  pricing filters. An absent configured OpenAI model selects the first usable
  catalog model. OpenRouter keeps its current default. `models` provider pins
  remain OpenRouter-only and produce a clear error in OpenAI mode. See
  [models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference).
- **Stateless HTTP requests.** Send `store: false`, `stream: true`, the system
  prompt as `instructions`, and the complete projected input on every OpenAI
  request. Put Ox's function tools in the `ox` namespace with `strict: false` so
  existing optional tool arguments retain their meaning. Preserve tool call IDs
  and the namespace when encoding calls and outcomes. Request encrypted
  reasoning content, save reasoning items as continuation metadata, and resend
  them beside the assistant message and tool calls. The selected catalog rejects
  a saved session from the other provider before its transcript is sent.
- **Only validated completions advance the prompt run.** Text and reasoning
  deltas remain provisional. Assemble tool arguments and final output, then
  validate `response.completed` before returning a completion. Failed,
  incomplete, cancelled, malformed, and prematurely ended streams never execute
  their proposed tools. Subscription-limit errors report the provider's error
  and the ChatGPT usage-settings URL. Preserve the existing explicit context
  overflow retry after successful compaction.
- **Summarizers use the subscription request format too.** OpenAI omits the
  unsupported `max_output_tokens`; retain Ox's 4,096-token summary allowance as
  a budgeting target and add that target to the summarizer instructions. Measure
  actual summaries before committing a compaction checkpoint. Keep OpenRouter's
  existing output limit. The
  [preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)
  govern request fields and supported tools.
- **Unknown price is absent.** Change catalog prices and `ModelUsage::cost` to
  optional values. OpenRouter supplies its reported values; OpenAI retains token
  usage without assigning a dollar cost. Cost aggregation ignores absent costs,
  and ACP omits absent model prices and session cost. Existing terminal
  rendering already handles these absences.

## Naming

Existing terms retain their definitions in `agents/glossary.md`, with its
OpenRouter-specific definitions widened only where needed for this change.

- **Model provider** — OpenRouter or OpenAI, selected once for an Ox server
  process. `Provider::{OpenRouter, OpenAI}` is the concrete enum.
- **Model client** — the selected provider's HTTP client, represented by
  `model::Client::{OpenRouter, OpenAI}`.
- `model::CatalogModel`, `ModelRequestParameters`, `Completion`, `Stop`, and
  `StreamItem` — the existing types moved out of `openrouter.rs`.
- `model::CompletionStream` — the enum whose `next` method returns shared
  `StreamItem` values from either provider's stream.
- `openai_auth::Credentials` — the single saved OpenAI registration, verified
  account identity, granted scopes, tokens, and expiry information.
- **Model request** and **model catalog** — retain their existing meanings,
  extended to the selected model provider.

## Test plan

- `main.rs`: valid login/logout commands require either provider; bare commands
  and unknown providers fail. Cover both top-level commands and ACP aliases.
- `settings.rs` and `lib.rs`: global provider selection controls catalog loading
  and defaults; workspace provider selection and OpenAI provider pins fail;
  OpenAI startup without credentials names the correct login command. Preserve
  existing OpenRouter settings guarantees.
- `openai_auth.rs`: a local scripted authorization/token service covers first
  registration and reauthorization, state mismatch, denied consent, invalid
  signature/issuer/audience/nonce/expiry, changed account identity, missing plan
  permission, and incomplete registration. A rejected attempt does not replace
  existing credentials. Credential tests cover owner-only atomic writes, logout,
  expiry, and serialized refresh using the latest rotating token.
- `openai.rs`: a scripted HTTP fixture covers catalog decoding, namespace tool
  schemas, ordered text/images, skill invocations, subagent messages, reasoning
  items, tool calls, and corresponding outcomes on the next request. Ordinary
  and summarizer requests obey subscription-route requirements.
- `openai.rs`: stream cases cover text and reasoning deltas, fragmented and
  multiple tool calls, reported token usage, refusal, explicit context overflow,
  subscription-limit failure after deltas, incomplete output, truncated streams,
  and stall timeout. Invalid terminal output never becomes a completion.
- `acp/prompt.rs`: use the OpenAI fixture for a tool turn followed by a final
  answer, a cancelled stream, a subagent turn, and compaction followed by
  another request. Verify saved batches, continuation metadata, provider
  selection, and discard of provisional output at the prompt-run boundary rather
  than repeating stream-parser assertions.
- `compaction.rs`: estimates use the selected provider's encoded bodies and
  image allowance. Summarizer requests use the same provider; an oversized
  summary cannot produce an invalid checkpoint. Existing cut and skill
  guarantees remain.
- `sessions.rs` and `acp/convert.rs`: token counts survive absent cost; session
  and child-session aggregation retains reported OpenRouter costs and leaves
  wholly unpriced usage absent. ACP catalog metadata omits absent prices.
- `acp.rs`: OpenRouter's advertised terminal-login arguments include the
  provider; logout affects only the selected provider and clears its cached
  model client.
- Live acceptance: explicit OpenAI login, account catalog discovery, a completed
  headless tool turn, a terminal turn, and compaction under the ChatGPT plan. A
  completed inference request verifies access; sign-in or catalog discovery
  alone does not. Report a live check that cannot run without user sign-in.

## Implementation plan

1. Create `crates/ox-server/src/model.rs`. Move shared types and the installed
   catalog there; add the two concrete dispatch enums and provider-specific
   transcript/body dispatch. Move shared skill-invocation and subagent-message
   text construction there. Keep OpenRouter-specific parsing and encoding in
   `openrouter.rs`. Expose the model provider enum through `lib.rs` for the CLI.
   Keep existing public test-fixture callers working with OpenRouter clients.
2. Modify `settings.rs` to read the global provider before catalog loading, then
   validate settings against the selected catalog. Keep provider selection out
   of session settings. Reject workspace selection and OpenAI provider pins,
   select provider-appropriate defaults, and preserve settings-file keys on
   save.
3. Create `openai_auth.rs` using the existing HTTP, UUID, JSON, base64, and
   filesystem facilities. Add dependencies in `crates/ox-server/Cargo.toml` for
   SHA-256 and JWT signature verification, and enable Tokio networking for the
   callback listener; update `Cargo.lock`. Implement the documented browser flow
   with a listener bound only to `127.0.0.1`, fresh state/nonce/verifier, and
   the exact callback URI used for code exchange. Persist the host ID separately
   from the owner-only credential record under `~/.config/ox/`. Implement token
   refresh and explicit logout. Keep credentials out of transcript entries,
   error details, and tool child processes. Open the browser directly without
   putting a retained ID token in a shell command or printed authorization URL.
4. Modify `main.rs`, `lib.rs`, and `auth.rs` for mandatory provider arguments
   and provider-specific login/logout. Login must work before provider settings
   or credentials exist. Update help text, aliases, and command-parser tests.
5. Create `openai.rs`. Fetch the authenticated catalog and encode Responses
   requests, including namespaced tools and reasoning continuation. Implement
   streamed assembly, completion validation, token usage, and provider errors.
   Include local scripted fixtures with injected endpoints and credentials;
   tests never use the real account or auth service.
6. Modify `sessions.rs` for optional cost, update cost aggregation and affected
   fixtures, and keep continuation metadata opaque. Modify OpenRouter parsing to
   populate known prices and costs. Do not migrate existing databases.
7. Modify `lib.rs` and `acp.rs` to start with the selected catalog and model
   client. OpenAI fails before ACP startup without authentication. Retain
   OpenRouter's lazy credentials, update its terminal-authentication arguments,
   and dispatch ACP logout to the selected provider. Omit absent prices from
   model config-option metadata.
8. Modify `acp/prompt.rs`, `acp/convert.rs`, and `subagents.rs` to use the
   shared model types and model client. Preserve prompt-run and assistant-batch
   lifecycles. Update their in-module tests and cost fixtures.
9. Modify `compaction.rs` so projection, image accounting, entry sizing,
   summarizer sizing, and summary requests use the selected provider's encoding.
   Add the OpenAI summary-budget instruction in its body builder. Save only
   reported summarizer costs and checkpoints verified against actual request
   size. Add the boundary tests above.
10. Correct the affected documentation passages listed below and perform the
    live acceptance checks after local fixture tests pass.

## Documentation updates

- `README.md`: correct Quick Start authentication, command syntax and ACP
  aliases, global provider selection, provider-specific model IDs/defaults,
  OpenAI login-before-startup setup, and OpenRouter-only provider pins.
- `AGENTS.md`: correct the existing Ox server directory-map entry to include
  both model providers.
- `agents/architecture.md`: correct the existing model-client and external
  boundaries, one-provider-per-process constraint, and the credential-refresh
  exception to process-local coordination and locks across asynchronous waits.
- `agents/glossary.md`: widen model request and model catalog definitions;
  define model provider and model client once, and update qualification rules.
- `agents/testing.md`: correct the existing scripted-provider and live-check
  passages to cover both providers.
