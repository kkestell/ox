# OpenAI subscription provider

## Scope and coverage

Reviewed the staged changes for
`agents/plans/2026-10-25-001-openai-subscription-provider.md`: the new
`internal/provider` and `internal/openai` packages, the provider-aware agent,
catalog, settings, keyring, and server code, the startup and `auth` commands in
`cmd/ox-server/main.go`, the status line in `internal/tui/view.go`, the tests,
and `README.md`. The review traced each OpenAI request from turn start through
credential loading, request encoding, and stream parsing, and checked the
callers of every changed shared type. It was judged against the plan, the work
log's departures, and the goal of keeping the change minimal.

Gaps:

- No live requests were made to OpenAI, and sign-in and sign-out were not run.
  The live behavior recorded in the work log was taken as given.
- Concurrent token refresh by two Ox processes was not tested. Whether OpenAI
  rejects a reused refresh token could not be confirmed without live requests.

## Summary

| ID | Severity | Category    | Fix   | Title                                                |
| -- | -------- | ----------- | ----- | ---------------------------------------------------- |
| F1 | Medium   | Correctness | Fixed | A new OpenAI session shows a `$0.00` cost            |
| F2 | Low      | Simplicity  | Fixed | The OpenAI stream tracks completion with a dead flag |

## F1. A new OpenAI session shows a `$0.00` cost

Medium · Correctness · Fixed · `internal/tui/view.go:318`

The plan says the status line omits the cost when the session has none. Before
the first response, the client has no usage update, and `usageSummary(nil)`
returned `0% • $0.00`. A new OpenAI session therefore showed `$0.00` until its
first response, then dropped the cost.

### Evidence

`usageSummary` returned the cost text for `nil` usage, and the test asserted
`usageSummary(nil) == "0% • $0.00"`. The server sends no usage update for a
session without an assistant message (`transcript.UsageSummary` returns `ok`
false), so every new session starts from `nil` usage.

### Fix

`usageSummary` shows the cost only when a usage update carries one, and `nil`
usage shows `0%`. A new OpenRouter session now also shows `0%` until its first
response reports a cost. The unit test and the terminal test's startup wait now
expect `0%`.

## F2. The OpenAI stream tracks completion with a dead flag

Low · Simplicity · Fixed · `internal/openai/stream.go:31`

`Stream` kept a `done` flag to return an error if `Next` was called after the
completion. The agent stops calling `Next` when it receives the completion, and
a stray call would already fail because the stream ends after
`response.completed`. The flag added a field and an error path with no behavior
any caller uses.

### Fix

Removed the `done` field and its check.

## Checks run

- `make check` — Passed.
- `make e2e` — Passed.

## Verdict

The change meets the plan with proportionate code: the provider interface is
small, OpenRouter behavior is unchanged apart from the shared types, and the
OpenAI client covers the planned sign-in, refresh, catalog, request, and stream
behavior with focused tests. F1 aligned the status line with the plan and F2
removed dead code. Both are fixed.
