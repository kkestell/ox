# Retry temporary model request failures

## Goal

OX-0026: one OpenAI 503 (`subscription_sharing_user_unavailable`) ended a
subagent's turn after 19 edits. Today the prompt run retries only OpenRouter
stalls. When this work is done, a model request that fails with a temporary
failure is retried for either model provider, after a short delay, up to the
existing attempt limit. Any other failure still ends the turn on the first
attempt.

## Related code

- `crates/ox-server/src/acp/prompt.rs:30` — `MODEL_REQUEST_ATTEMPTS`, the
  attempt limit, documented as OpenRouter-only.
- `crates/ox-server/src/acp/prompt.rs:413` — `run_model_step`, whose retry loop
  matches `io::ErrorKind::TimedOut` only for `model::Provider::OpenRouter`. The
  main agent and subagents both run this loop.
- `crates/ox-server/src/model.rs:279` — `InputContextOverflow` and
  `is_input_context_overflow`, the existing marker pattern for a model request
  failure the prompt run handles specially.
- `crates/ox-server/src/openrouter.rs:312` — `stream_body` turns a failed HTTP
  status into an error through `status_error`.
- `crates/ox-server/src/openai.rs:190` — `stream_body` turns a failed HTTP
  status into an error through `provider_error`.
- Tests that script a 500 to make a model request fail:
  `crates/ox-server/src/acp/prompt.rs:1742`,
  `crates/ox-server/src/subagents.rs:864` and `:911`,
  `crates/ox-server/src/acp.rs:3102`, `crates/ox/src/acp.rs:788`, and
  `crates/ox/tests/tui.rs:703`.

## Decisions

- **Temporary failures are a stall or an HTTP 429 or 5xx status.** The observed
  failure was a 503 before any response data, and rate limits and server errors
  are the statuses a later attempt may not repeat. Transport errors and error
  events inside a stream have not been observed and stay final.
- **One rule for both model providers.** The OpenRouter-only check goes away, so
  OpenAI stalls are now retried too. This reverses the behavior that
  `openai_stalls_discard_the_attempt_without_retrying` held; OX-0026 asks for
  OpenAI retries, and one rule is less code than two.
- **Retry in the prompt run, not in the model clients.** The stall retry already
  lives in `run_model_step`, and a stall can happen after provisional output,
  which only the prompt run can handle. Compaction summarizer requests stay
  unretried.
- **Wait 2 seconds before the second attempt and 8 before the third.** Retrying
  a 503 at once tends to fail the same way. The wait observes cancellation.
  Tests build with zero delays through `#[cfg(test)]`, because the scripted
  fixtures use real sockets and paused tokio time would fire stall timeouts
  early.
- **Keep the provider's message.** The marker carries the message the provider
  error already builds, so the final error reads exactly as today.

## Naming

- `temporary failure` — a model request failure that a later attempt may not
  repeat: a stall, or an HTTP 429 or 5xx status. Used in the comments on
  `model::Temporary`, `model::is_temporary`, and `RETRY_DELAYS`.
- `model::Temporary` — the marker error wrapping a temporary failure's message,
  following `InputContextOverflow`.
- `model::is_temporary` — true for a stall (`io::ErrorKind::TimedOut`) or a
  `model::Temporary` error.
- `model::status_error` — wraps a failed HTTP status's error in
  `model::Temporary` when the status is 429 or 5xx, and returns it unchanged
  otherwise.
- `RETRY_DELAYS` — the waits before the second and third attempts.

## Test plan

- `crates/ox-server/src/acp/prompt.rs`: rename
  `stalled_model_requests_are_retried_up_to_the_attempt_limit` to
  `temporary_failures_are_retried_up_to_the_attempt_limit` and add table cases:
  a 503 then an answer finishes after two requests; a 429 then an answer
  finishes after two requests; three 503s fail after three requests with the 503
  in the error; a 400 fails after one request. Keep the two stall cases.
- `crates/ox-server/src/acp/prompt.rs`: replace
  `openai_stalls_discard_the_attempt_without_retrying` with
  `openai_temporary_failures_are_retried`: a 503 carrying
  `subscription_sharing_user_unavailable`, then a stall, then an answer,
  finishes after three requests and saves one assistant batch.
- Switch the tests listed under Related code from 500 to 400, since each means a
  failed request rather than a temporary failure. Update the
  `OpenRouter returned 500` assertion in `subagents.rs:916` to 400. Leave
  `crates/ox-server/src/compaction.rs:1156` at 500, because summarizer requests
  are not retried.

## Implementation plan

1. In `crates/ox-server/src/model.rs`, add `Temporary(String)` with `Display`
   and `Error` beside `InputContextOverflow`, `is_temporary`, and
   `status_error(status: reqwest::StatusCode, error: io::Error) -> io::Error`.
2. In `crates/ox-server/src/openrouter.rs` `stream_body`, pass the status error
   through `model::status_error` after the context overflow check.
3. In `crates/ox-server/src/openai.rs` `stream_body`, pass the `provider_error`
   result through `model::status_error`.
4. In `crates/ox-server/src/acp/prompt.rs`, document `MODEL_REQUEST_ATTEMPTS` as
   the limit for temporary failures, add `RETRY_DELAYS` with its test variant,
   and change the retry guard in `run_model_step` to
   `model::is_temporary(&error) && attempts < MODEL_REQUEST_ATTEMPTS`. Before
   each retry, wait `RETRY_DELAYS[attempts - 1]` in a `tokio::select!` that
   returns `PromptOutcome::Cancelled` on cancellation. Update the doc comment of
   `run_model_step` and the stall comment inside the loop.
5. Update the tests as the test plan describes.
6. Mark OX-0026 fixed in `agents/issues.csv` and check it off in
   `agents/todo.md`.
