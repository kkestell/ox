# Codex contribution fixes

## Scope and coverage

The uncommitted changes in the `codex-fixes` worktree on base `d79ffe6`,
against [plan 2026-11-22-002](../plans/2026-11-22-002-codex-contribution-fixes.md),
which covers F1–F10 of
[audit 2026-11-22-002](../audits/2026-11-22-002-codex-contributions.md). The
tree holds the fifteen modified files of the plan and the untracked plan and
audit.

The review traced each decision of the plan through the code it names:
`creation`, `canCreate`, and `recoverable` in `ox.tsx` and their readers in
`App.tsx` and `NewSessionView.tsx`; `PromptBubble` and the `Prompt` type;
`listen` and the new lock test; the `session_id` rows; Ur's `conversation`,
`request`, and `Handle(ended)`; the split workspace specs; the new recovery
spec; the stack test script; and the `/ox-audit` skill text. It also searched
for structures of the same kind as F1–F10 left in the client and in Ur.

Gaps:

- `docs/todo.md` in this worktree has no entries for this audit. The audit
  wrote them into the main checkout's uncommitted `docs/todo.md`
  (`/Users/kyle/src/ox/docs/todo.md:23-49`), so plan step 10 has nothing to
  remove here. They come out when this change lands on `main`.
- No conditions were removed to confirm that the new tests fail without them;
  the user asked for no mutation checks. The trace below stands in for them.
- Outside the plan: whether a session can be unarchived is decided both in
  `Sidebar.tsx:227` and in `SessionView.tsx:57`, the same shape as audit F7.
  It comes from `15d5cc5`, which is not a Codex commit, so it is outside this
  audit's scope and is left for the next `/ox-audit`.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                                  |
| -- | -------- | ------------ | -------- | ------ | ------------------------------------------------------ |
| F1 | Low      | Simplicity   | Rejected | —      | `conversation` guards a history that starts with a reply |
| F2 | Low      | Construction | Rejected | —      | `create` checks `free()` instead of `canCreate()`      |
| F3 | Low      | Testing      | Rejected | —      | The recovery spec marks the `existing` reply itself    |
| F4 | Low      | Intent       | Rejected | —      | The held session list leaves the malformed-link test   |

## F1. `conversation` guards a history that starts with a reply

Low · Simplicity · Rejected · `internal/ur/ur.go:267`

`conversation` checks `last >= 0` before joining an assistant entry to the
message before it. Ur always saves the user's prompt before any reply, so a
history Ur wrote never starts with an assistant entry, and no test reaches the
check.

### Evidence

`begin` saves the prompt (`ur.go:241`) before `stream` starts, and `delta`
saves only for the running turn. `load` reads the file without checking its
order.

### Action

Rejected. The check costs one comparison and keeps `conversation` defined for
every `[]message`, including one `load` reads from a file that starts with an
assistant line. Without it, such a file panics `oxd` on the first request.
Removing it saves nothing.

## F2. `create` checks `free()` instead of `canCreate()`

Low · Construction · Rejected · `cmd/ox/web/src/ox.tsx:267`

The plan says `create` checks `canCreate()`. It checks `free()`, and the
control exposes `canCreate: free`.

### Evidence

`ox.tsx:239` binds `canCreate` to the same function `create` calls, so the
view and `create` read one rule.

### Action

Rejected. Both names reach one function, which is the plan's decision. Inside
the control, `free()` reads the same as `canSend` and `canConfigure` do.

## F3. The recovery spec marks the `existing` reply itself

Low · Testing · Rejected · `cmd/ox/web/e2e/session.spec.ts:840-851`

"a retried creation recovers the session without sending" adds its own
EventSource init script, beside `cutReply`'s, that sets `data-existing` when a
reply carries `existing: true`.

### Evidence

Replies arrive over the relay's event stream, which `page.route` does not see.
The test must release the held `session/get` only after the retry's reply, or
the session loads while the draft is `"creating"` and the path the test names
is reached in an order it does not claim. No page state marks that reply
alone: after it, the draft looks as it did after the first failure.

### Action

Rejected. The marker waits for the event the test claims to test, as
`AGENTS.md` asks. Folding it into `cutReply` would join two unrelated helpers.

## F4. The held session list leaves the malformed-link test

Low · Intent · Rejected · `cmd/ox/web/e2e/workspace.spec.ts:9-42`

The malformed-link test no longer holds `workspace/list`; "a draft holds Send
until the session list loads" holds it instead, and asserts Send enabled after
the release where the old test asserted the permission mode.

### Evidence

Plan step 7 lists the malformed-link test as "open `/#%`, add `/`, and see it
in the sidebar", and the held list under the second test. The permission mode
is checked by "a draft keeps its permission mode through a relay restart".

### Action

Rejected. This is the split the plan decided, and each behavior keeps a test
named for it.

## Checks run

- `make test`: passed, including the stack test "one terminal Ctrl-C signals
  each service once".
- `OX_LIVE=1 go test -race -count=1 -run TestLiveUr -v ./internal/server`
  with `.env`: passed. Its second turn, after parking, resumes from the file
  through `conversation` and answers from the first turn.
- `make e2e`: taken from `/ox-work`'s report (46 passed). A rerun here was
  stopped by the user.
- `make live` beyond `TestLiveUr` and a headless Chrome check: not required.
  The change alters neither how Ox drives Claude Code nor how the page reaches
  `oxd`.

## Verdict

Ready to commit. No finding needs a fix; F1–F4 are rejected.
