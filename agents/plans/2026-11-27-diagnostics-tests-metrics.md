# Diagnostics, tests, and metrics

## Goal

Resolve [#156](https://github.com/kkestell/ox/issues/156), findings F24, F29,
F32, F33, F35, and F37 of
[the 2026-11-26 audit](../audits/2026-11-26-main.md):

- An Ur reply that OpenRouter cuts off at the output limit, filters, or ends
  with an error fails its turn, and a one-shot `ask`, with a plain reason.
- `Agent.Send`'s doc states only the rules every harness shares.
- Every failed SQLite write in session create, child create, prompt images,
  archive, and delete reaches `oxd`'s log, naming the session.
- The live "plan-mode message asks" case is skipped, not passed, when Haiku
  declines the call.
- `make metrics` counts only Ox's Go module.
- The one-shot notes in `docs/harnesses/claude-code.md` say what `initialize`
  validates as observed.

## Related code

- `internal/ur/engine/stream.go` `read` — parses the SSE stream for both the
  session turn (`engine.stream`) and the one-shot `ask`. Sets `finished` on any
  nonempty `finish_reason`.
- `internal/ur/engine/engine.go` `handle` (`ended`) — a non-nil error ends the
  turn with `agent.Result{Text: err.Error(), IsError: true}`; streamed partial
  text is already saved through `delta`.
- `internal/ur/ur_test.go` `TestStreamErrors`, `TestAsk` — table of failed
  streams with the next request's roles; `Ask` against an `httptest` server.
- `internal/agent/agent.go` `Agent.Send` doc — describes Claude's delayed write
  as universal. `internal/claude/prompt.go` and `internal/ur/agent.go` `Send`
  already state their own write timing.
- `internal/server/methods.go` `addSession`, `internal/server/mcp.go`
  `addChild`, `internal/server/session.go` `sendPrompt` (`AddImages`),
  `setArchivedLocked` (`SetArchived`), `finishDelete` (`DeleteSession`) — return
  store errors without logging. `session.go` already logs
  `session %s: store title: %v` and `session %s: store settings: %v`.
- `internal/server/worktree_test.go` `TestWorktreeDeleteRepeated` — injects a
  refused row delete with a SQLite trigger. `mcp_test.go` captures the log with
  `lockedBuffer` and `log.SetOutput`.
- `internal/server/live_test.go` section "plan-mode message asks" — the
  `default` branch logs a declined call and passes.
- `scripts/metrics.py` — `classify` takes every tracked `.go` file;
  `go_complexity` runs gocognit over the whole snapshot, which includes the
  `docs/screenshots/repos/field-sync` module.
- `docs/harnesses/claude-code.md` §12 — tells the reader to send `initialize`
  to validate model and effort; §2 records that `initialize` accepts an unknown
  model and an unknown effort runs at the default.

## Decisions

- **Finish reasons.** `read` keeps the last nonempty `finish_reason` and, at
  the end of the stream, accepts only `stop` and `tool_calls`, OpenRouter's
  normalized success values. Every other value is an error, so an unrecognized
  reason fails clearly rather than passing as a reply:
  - `length`: `OpenRouter stopped the reply at the model's output limit`
  - `content_filter`: `OpenRouter's provider filtered the reply`
  - `error` without an error chunk: `OpenRouter ended the reply with an error`
  - any other: `OpenRouter ended the reply with finish reason "<reason>"`

  An error chunk is still checked first, so `finish_reason: "error"` with a
  message keeps `OpenRouter: <message>`. No finish reason keeps
  `OpenRouter stream ended before the response finished`. Tool calls in a
  failed reply are dropped, as for every other stream error.
- **Requirements.** **Failed turns** and **Ur sessions**: a cut-off or filtered
  Ur reply now ends as a failed turn showing its reason, with its partial text
  kept in the conversation, through the existing `ended` error path. Parking and
  resuming carry the partial text as they do for the other stream errors.
  **Database failures**: the five writes join the paths that already log; each
  still returns its error to the requester.
- **Log lines.** Each is logged where the write happens, naming the session by
  ID in the existing form:
  - `addSession` and `addChild`: `session <id>: store session: <err>` (the new
    session's ID).
  - `sendPrompt`: `session <id>: store images: <err>`.
  - `setArchivedLocked`: `session <id>: store archived: <err>` with
    `members[0].id`, the session the request named.
  - `finishDelete`: `session <id>: delete: <err>`, for the `DeleteSession`
    error only. A worktree removal failure is a git failure, not a database
    write, and keeps its current handling.
- **Send doc.** The interface states: Send accepts the input behind every input
  the harness has not taken, and the harness takes them in that order; a
  `Tracker` keeps them, publishes each input's lifecycle updates, and derives
  the published state; Send returns `ErrExited` once the agent has ended, or the
  error of a failed write, and then publishes and keeps nothing; each harness
  documents when it writes the input to its process. Ur's `Send` comment gains
  "at once" so its timing is explicit.
- **Live skip.** The declined branch keeps its check that no message reached
  the recipient, then calls `l.t.Skipf`. Sections are subtests, so only that
  section is skipped.
- **Metrics.** A Go file is measured only when no `go.mod` other than the root
  one sits in a directory above it; nested modules come from
  `git ls-files '*/go.mod'`. gocognit runs on the measured production Go files,
  passed as absolute paths in the snapshot, instead of the whole snapshot, so
  sizes, suppressions, and complexity share one file set.
- **Claude Code.** No new assumption. The §12 correction rests on §2's
  recorded `initialize` and `--effort` behavior and §12's recorded plain-stdin
  `--output-format json` result.

## Check your assumptions

None.

## Test plan

- `length`, `content_filter`, and `error` without an error chunk each fail the
  session turn with their reason, and the next request carries the partial
  reply (`user`, `assistant`, `user`): three new cases in `TestStreamErrors`
  (Ur, `internal/ur/ur_test.go`), each streaming `"par"` then a chunk with the
  finish reason and `[DONE]`.
- A one-shot `Ask` whose stream ends with `length` returns the length error:
  new case in `TestAsk`, keyed on a model name the handler answers with that
  stream.
- A refused row delete is logged with the session's ID: extend
  `TestWorktreeDeleteRepeated` to capture the log with `lockedBuffer` and assert
  it contains `session <id>: delete:` and `refused`.
- The plan-mode skip and the Send doc have no automated test; `make live` runs
  the skip path when Haiku declines.
- Metrics: run `make metrics` before and after; Go code and test counts drop by
  the fixture's lines, and no `field-sync` path appears. The script has no test
  harness, and this check observes the printed numbers directly.

## Implementation plan

1. `internal/ur/engine/stream.go`: replace `finished` with the last nonempty
   `finish_reason`; after the loop, map it as in Decisions. Update `read`'s
   comment to say which finish reasons complete a reply.
2. `internal/ur/ur_test.go`: add the `TestStreamErrors` and `TestAsk` cases.
3. `internal/agent/agent.go`: rewrite the `Send` doc as in Decisions.
   `internal/ur/agent.go`: say Send writes the input to ur at once.
4. `internal/server/methods.go`, `internal/server/mcp.go`,
   `internal/server/session.go`: add the five log lines.
5. `internal/server/worktree_test.go`: extend `TestWorktreeDeleteRepeated`.
6. `internal/server/live_test.go`: in "plan-mode message asks", skip the
   declined branch with `l.t.Skipf` after its recipient check, and update the
   section's comment.
7. `scripts/metrics.py`: exclude Go files under nested modules in `classify`
   (or where `measured` is built), and pass the measured production Go files to
   gocognit.
8. `docs/harnesses/claude-code.md` §12: replace the `initialize` bullet with:
   "`initialize` validates neither model nor effort: Claude accepts an unknown
   model until the prompt, whose `result` reports the refusal, and runs an
   unknown effort at its default (§2). Send the prompt, read `result`, and close
   stdin." Run `make docs`.
9. Run `make test`, `make live` (for `TestLiveUr`, which shows the built-in
   model's normal replies still finish, and the plan-mode section), and
   `make metrics`.

## Requirement changes

None.

## Documentation updates

- `docs/harnesses/claude-code.md` §12, as in task 8.
