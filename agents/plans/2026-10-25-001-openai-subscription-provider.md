# OpenAI subscription provider

## Goal

Let a user run `ox auth login openai`, sign in with their ChatGPT account in the
browser, and use OpenAI models on their Codex subscription alongside OpenRouter
models. The model picker lists both providers' models.

## Related code

- `cmd/ox-server/main.go` owns `auth login/logout` and startup.
- `internal/keyring/keyring.go` stores one fixed OpenRouter account.
- `internal/openrouter/` owns the catalog, request encoding, streaming, and the
  request and completion types that `internal/agent/agent.go` uses.
- `internal/catalog/catalog.go` treats every model as an OpenRouter model.
- `internal/settings/settings.go` defaults to an OpenRouter model.
- `internal/server/convert.go` sends model prices to ACP clients;
  `internal/tui/picker.go` already shows a provider column and absent prices.

## Decisions

### Provider abstraction

Create `internal/provider` with the types the agent needs from a provider:
`Request`, `Completion`, `Item`, `Stop`, `ErrContextOverflow`, and
`IsTemporary`, all moved out of `internal/openrouter`. Its interface is:

```go
type Provider interface {
	FetchCatalog(ctx context.Context) (catalog.Catalog, error)
	Stream(ctx context.Context, r Request) (Stream, error)
}
```

`Stream` has the existing `Next()` and `Close()`. The agent holds a map from
provider name to `Provider` and uses the selected model's provider for the whole
turn, including compaction. Tool schemas stay in their current format; the
OpenAI client translates them.

### Catalog

Add a `Provider` field to `catalog.Model`. Qualified IDs stay
`<provider>:<model-id>`, and `Lookup` compares both parts. Prices become
`*float64` so that OpenAI models have none. Settings that pin OpenRouter
providers to a model stay OpenRouter-only. When the built-in default model is
not in the catalog because OpenRouter is not configured, use the first catalog
model.

### Login

Use the same OAuth client that the Codex CLI uses:

- Client ID `app_EMoamEEZ73f0CkXaXp7hrann`.
- Authorize at `https://auth.openai.com/oauth/authorize` with
  `response_type=code`, PKCE S256, `state`, scope
  `openid profile email offline_access`, `id_token_add_organizations=true`,
  `codex_cli_simplified_flow=true`, and `originator=ox`.
- Redirect to `http://localhost:1455/auth/callback`, served by a listener on
  `127.0.0.1:1455`.
- Exchange the code at `https://auth.openai.com/oauth/token`.

Print the authorization URL and open it with `open` or `xdg-open`. Check
`state`, exchange the code, and read the ChatGPT account ID from the access
token's `https://api.openai.com/auth` claim (`chatgpt_account_id`). Save
`{access, refresh, expires, account_id}` as JSON in the keyring under a second
account, `OPENAI_AUTH`. Logout deletes that entry. `internal/openai/auth.go`
owns this.

Change the keyring functions to take the account name.

### Token refresh

Before each OpenAI request, read the credential from the keyring. If it has
expired, refresh it with `grant_type=refresh_token`, the client ID, and the
refresh token, then save the result. Reading the keyring for each request picks
up a refresh made by another Ox process. If the refresh fails, return an error
that tells the user to run `ox auth login openai`.

### Catalog fetch

Fetch `GET https://chatgpt.com/backend-api/codex/models` with the access token
and the `chatgpt-account-id` header. Keep entries whose `visibility` is `list`,
in the order returned. Use `slug` as the ID, `display_name` as the name, and the
context window, reasoning levels, and input modalities to fill `ContextLimit`,
`Efforts`, and `AcceptsImages`. Confirm the response's field names with one live
call before writing the parser, and save a cleaned copy as the test fixture.

### Requests

`POST https://chatgpt.com/backend-api/codex/responses` with headers
`Authorization: Bearer <access>`, `chatgpt-account-id`,
`OpenAI-Beta: responses=experimental`, and `originator: ox`. The body is:

- `model`, `instructions` (the system prompt), and `input` (the full
  transcript);
- `tools` as plain `function` tools translated from the existing schemas,
  `tool_choice: "auto"`, and `parallel_tool_calls: true`;
- `store: false` and `stream: true`;
- `include: ["reasoning.encrypted_content"]`;
- `reasoning: {effort, summary: "auto"}` when the effort is not the default;
- `prompt_cache_key` set to the session ID.

Translate the transcript the same way `chatMessages` does for OpenRouter. User
input, compaction summaries, and hook context become user messages. Assistant
text becomes an assistant message. Tool calls become `function_call` items. Tool
outcomes become `function_call_output` items.

Save the response's reasoning items as the assistant message's continuation
metadata, and send them back before that message's text and calls. Each provider
sends continuation metadata only from turns that used the requested model, taken
from the preceding `TurnStart`. This keeps one provider's reasoning data out of
another provider's requests.

Read the stream events:

- `response.output_text.delta` streams text.
- `response.reasoning_summary_text.delta` streams reasoning.
- `response.output_item.done` collects messages, reasoning, and function calls.
- `response.completed` finishes the response and reports usage.
- `response.incomplete` caused by the output token limit stops with the token
  limit.
- `response.failed` and `error` are errors.

Map a context-length error to `ErrContextOverflow`. Treat 5xx statuses and 429
statuses as temporary, except the usage-limit error. That error is reported with
a pointer to the user's ChatGPT usage settings and is not retried. Reuse the
stall timeout.

### Usage

Save `input_tokens`, `input_tokens_details.cached_tokens`, `output_tokens`, and
`output_tokens_details.reasoning_tokens` from `response.completed` as the
message's usage, with no cost. The existing context usage then works unchanged.
The status line omits the cost when the session has none, instead of showing
`$0.00`.

## Implementation plan

1. Create `internal/provider/provider.go`. Move the shared types out of
   `internal/openrouter` and change `internal/agent/agent.go` to choose a
   provider by model. Update the OpenRouter, agent, server, and client tests.
2. Add the provider field to `internal/catalog/catalog.go` and make prices
   optional. Update `internal/openrouter/catalog.go`,
   `internal/settings/settings.go`, and `internal/server/convert.go`.
3. Make `internal/keyring/keyring.go` take an account name.
4. Create `internal/openai/auth.go`, `client.go`, `messages.go`, and
   `stream.go`, with tests that use local HTTP servers.
5. In `cmd/ox-server/main.go`, accept `openai` in `auth login/logout` and build
   each provider that has credentials at startup. Concatenate the catalogs with
   OpenRouter first. Fail with login instructions when no provider has
   credentials. Update the help text in `cmd/ox/main.go`.
6. In `internal/tui/view.go`, omit the cost from the status line when it is
   absent.
7. Add OpenAI login and logout to `README.md`.

## Test plan

- Request encoding from a transcript with text, images, tool calls and outcomes,
  hook context, compaction, and saved reasoning. Reasoning from a different
  model is left out.
- Stream parsing for text, reasoning summaries, several function calls, usage,
  the output token limit, failures, the usage limit, and an early end of stream.
- Catalog parsing from the saved fixture.
- Refresh when the saved credential has expired, and no refresh before then.
- Lookup with the same model ID under both providers; the agent uses the
  selected model's provider.
- Manually: log in, list the models, run a turn that calls a tool, restart Ox,
  and log out.
