# Review: syntax highlighting in the file viewer

## Scope and coverage

The uncommitted changes on base `32db923` (the working tree as of
2026-11-20): `cmd/ox/web/src/highlight.ts`, `FileView.tsx`, `style.css`,
`package.json`, `package-lock.json`, `e2e/workspace.spec.ts`,
`docs/architecture.md`, and `docs/functional-requirements.md`, reviewed
against `agents/plans/2026-11-20-013-file-highlighting.md`.

Gaps:

- No `/ox-work` final response came with the request, so its Tests proved and
  Checks could not be compared. `make test` and `make e2e` were run here
  instead, and the memos were removed one at a time to see what the tests
  catch.

## Summary

| ID | Severity | Category    | Action   | Status   | Title                                              |
| -- | -------- | ----------- | -------- | -------- | -------------------------------------------------- |
| F1 | Medium   | Correctness | Fix      | Resolved | The pane shows "undefined" until the first read    |
| F3 | Low      | Testing     | Fix      | Resolved | F1's test can hold the previous file's poll        |
| F2 | Low      | Testing     | Rejected | —        | No test fails when `text` is not a memo            |

## F1. The pane shows "undefined" until the first read

Medium · Correctness · Fix · `cmd/ox/web/src/FileView.tsx:20`

The `html` memo returns `undefined` until the file's first read arrives, and
Solid compiles `innerHTML={html()}` to `el.innerHTML = value`. The DOM's
`innerHTML` setter converts `undefined` to the string `"undefined"`, so every
time a file opens, the pane shows the word "undefined" until `oxd` answers.
Before this change, `{text()}` rendered nothing in that interval.

### Evidence

The built bundle sets `e.innerHTML=t` in the fallback's effect with no
undefined check. A probe spec held the first `workspace/read_file` after a
file click and read the panel: `PANEL WHILE HELD: "undefined"`.

### Action

Make `html` return `""` while `text()` is `undefined`, so the pane is empty
until the first read. Keep an e2e check in "the file viewer shows each
session's open file": hold the first `workspace/read_file` for `main.go` with
`hold(page, "workspace/read_file")` before clicking its row, assert the panel
has text `""` while the request is held, then release it and continue with
the existing assertions.

## F2. No test fails when `text` is not a memo

Low · Testing · Rejected · `cmd/ox/web/src/FileView.tsx:16`

The plan says the `data-marked` step fails if `text` is not a memo. It does
not: with `text` as a plain function, the `html` memo's string equality still
stops the unchanged HTML from reaching the DOM, and the test passes. With
`html` as a plain function, the `text` memo still prevents the effect from
running again. The step fails only when both memos are removed.

### Action

Rejected. The DOM behavior users see, unchanged content and scroll position
on an unchanged read, is protected: the step fails when both memos are
removed. What only the `text` memo adds is skipping a re-highlight on each
unchanged read, a CPU cost no public interface observes cheaply. Keeping the
code as it is costs less than building a test for that cost.

## Checks run

- `make test`: passed (Go tests, Biome, `tsc`, stack tests).
- `make e2e`: 40 passed, 3 skipped (the live specs).
- Probe spec that held the first `read_file`: the panel read `"undefined"` (F1).
- `text` as a plain function: "the file viewer shows each session's open
  file" passed. `html` as a plain function: passed. Both removed: failed at
  `span[data-marked]` (expected 1, received 0). The source was restored after
  each run.
- `make live` and a headless Chrome check through `make dev` were not needed:
  the change does not alter how Ox drives Claude Code or how the page reaches
  `oxd`.

## F3. F1's test can hold the previous file's poll

Low · Testing · Fix · `cmd/ox/web/e2e/workspace.spec.ts:352`

`hold(page, "workspace/read_file")` holds the next `read_file` of any path.
`main.txt` is open when the hold is set, so a poll tick for it in the window
before `main.go` is clicked is the request held. `main.go`'s first read then
goes through, and `toHaveText("")` passes only if it runs before that reply
lands. Otherwise the assertion sees the file's text and fails after its
timeout. In that case the test also no longer checks F1.

### Evidence

A one-second wait added after the hold, with request logging, showed the
held request was `src/main.txt` (id 35) and `src/main.go`'s first read went
through unheld. The test passed only because the empty-pane assertion ran
before that reply arrived.

### Action

Give `hold` a way to match the request's parameters: replace its
`sessionId` argument with `params?: Record<string, unknown>`, matched when
every given key equals the request's parameter, and change the three callers
that pass a session ID to `{ session_id: id }`. Hold `main.go`'s read with
`hold(page, "workspace/read_file", { path: "src/main.go" })`. Confirm the
change by adding a wait of more than a second after the hold and checking
that the test still passes and that the held request is `main.go`'s, then
remove the wait.

## Verification 1

The working tree on base `32db923` after the F1 fix.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `html` returns `""` before the first read. With `undefined` restored, the new held-read step fails: expected `""`, received `"undefined"`. |

New findings: F3.

Checks:

- `make e2e`: 40 passed, 3 skipped.
- `make test`: its gofmt step fails on `.hypercube/56479ae4-deslop-*/…_test.go`.
  Those are git-excluded worktrees another tool created during this review,
  not part of the change. Each step of `make test` run directly, with gofmt
  over the tracked Go files, passed: gofmt, `go mod tidy -diff`, `go fix -diff`,
  golangci-lint, Biome and `tsc`, the web build, `go test -race ./...`, and
  the stack tests.

## Verification 2

The working tree on base `32db923` after the F3 fix.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | Unchanged since verification 1. With `undefined` restored and a 1.2 s wait after the hold, the held-read step fails: received `"undefined"`. |
| F3 | Resolved | `hold` matches on a `params` subset, and the three `session/get` callers pass `{ session_id }`. `sessionId(page)` is never empty at those calls, so they keep their filter. With a 1.2 s wait after the hold, `src/main.txt`'s poll went through and `src/main.go`'s first read was held; the test passed. |

New findings: none.

Checks:

- `make test`: passed. The `.hypercube/` worktrees are gone.
- `make e2e`: 40 passed, 3 skipped.
- The injected waits and reverts were removed after each run.

## Verdict

Ready to commit.
