# OpenAI subscription provider

## Plan

`agents/plans/2026-10-25-001-openai-subscription-provider.md`

## Summary

`ox auth login openai` signs in through the browser and saves the sign-in in the
keyring. Startup builds a provider for each signed-in provider and lists
OpenRouter's models, then OpenAI's. Turns use the selected model's provider.
OpenAI requests go to the Codex backend and use the subscription. The goal is
met: a live headless turn on `openai:gpt-5.6-luna` called the shell tool and
answered correctly.

## Departures from the plan

- The models endpoint requires `client_version` and lists only the models whose
  minimum version is at most that value. Version `0.130.0` listed one hidden
  model. Ox sends `99.0.0` so that no model is hidden because of Ox's version.
- Requests also send a `session-id` header with the session ID. With only
  `prompt_cache_key`, three live requests in one session cached 0 tokens. With
  the header, the second request cached 9,600 of 9,983 input tokens.
- Requests always send `reasoning.summary: "auto"`, and add the effort only when
  it is not the default. Otherwise, default-effort turns would show no
  reasoning.
- `keyring.Set` rejects a macOS secret longer than `security -i` reads. The
  command silently truncates lines over 4,096 bytes, which would save a corrupt
  credential. The live sign-in record is about 2 KB.
- The built-in default model falls back to the first catalog model only when the
  catalog has no OpenRouter models. An OpenRouter catalog without the built-in
  model still fails as before.

## Decisions

- Access tokens are refreshed one minute before they expire, so a token does not
  expire during a request. Live access tokens last 10 days.
- A refresh response without a refresh token keeps the saved one.
- Reasoning items are saved without their `id`, since the backend stores no
  responses that an ID could refer to.
- Reasoning summary parts after the first are separated by a blank line.
- A keyring failure stops startup only when no provider has credentials. This
  lets an `OPENROUTER_API_KEY` run work in Docker, which has no keyring.
- `usage_limit_reached` and `usage_not_included` errors are not retried and
  point to the ChatGPT usage settings. Other 429 and 5xx errors stay temporary,
  as do `rate_limit_exceeded`, `server_error`, `server_is_overloaded`, and
  `slow_down` failures during a stream.
- The Codex `ultra` effort is dropped because Ox has no effort with that ID.

## Checks run

- `make check` — Passed.
- `make e2e` — Passed.
- `TestMCPFailedDeleteKeepsConnectionUsable` in `internal/server` failed once
  with "session has an operation in progress". It passed on three reruns and on
  the full `make check`.

## Manual verification

1. Sign in.

   ```sh
   make build && bin/ox auth login openai
   ```

   The browser opened, the callback page said "Signed in to Ox", and the keyring
   held the credential. Its access token lasts 240 hours.

2. List the models of both providers.

   ```sh
   bin/ox run --model openai:none x
   ```

   The error listed 150 OpenRouter models followed by 7 OpenAI models.

3. Run a turn that calls a tool, then restart and run another.

   ```sh
   bin/ox run --dir /tmp/ws --model openai:gpt-5.6-luna --effort low \
     "Use the shell tool to read every file in this directory, then reply with their contents joined by a comma."
   ```

   Both runs answered `alpha,beta` after shell calls. The saved batches hold
   encrypted reasoning items, which the following requests sent back without
   error. The usage has token counts and no cost.

Logout was not run, so the sign-in stays in place.

## Follow-up work

- Run `ox auth logout openai` and check that the keyring entry is removed.
