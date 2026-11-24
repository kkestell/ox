# Review: every session stores its directory

## Scope and coverage

Reviewed the uncommitted changes on branch `session-directory` against base `5d72dd5` (`git merge-base origin/main HEAD`, also `HEAD`): 21 modified files across `internal/store`, `internal/server`, the web client, its e2e specs, and `docs/`, plus the untracked plan. Checked against `agents/plans/2026-11-22-session-directory.md`, whose two product questions the user approved as planned and whose Requirement changes for **File tree** and **File viewer** the user approved word for word.

`docs/functional-requirements.md` carries exactly the plan's two requirement changes, word for word.

The earlier Codex review, `agents/scratch/session-worktrees-codex-review.md`, raised three findings that apply here. All three are resolved:

- **10 (selector keyed by directory):** the tree and viewer are keyed by `selected()`, so each selection mounts its own with its own `FileRoot`. `fileRoot` is the one server helper that resolves and checks the selector for both file methods. Keying by directory makes "each session keeps its own expanded folders" fail with `session not found` (below).
- **11 (empty directory, migration):** `AddSession` refuses a non-absolute directory, including `""`. The backfill copies the stored spelling as is. `SaveSession` is unchanged. The column check uses `QueryRow(...).Scan`, which releases the single connection before `Begin`. `TestUpgradeAddsSessionDirectory` covers an archived child, a resume token, a title, events, a rolled-back failed upgrade, and a reopen. Its `oldSchema` matches the base `schema` exactly.
- **12 (session-directory parts):** `newSession(record, workspace, firstLine)` keeps its signature and reads the directory from the record. `directory()` is gone. `cmd/ox-mcp`'s fixture creates its session through `session/create` and needed no change. The page sets `directory` on both the reload path and the `session/created` path.

The implementer's three open choices:

- Invalid-JSON params answer `-32602` with the selector message: see F4.
- The expanded-folders spec toggles the tree under the first session so that the tree mounts with that session's selector rather than the draft's, and runs clocked so it can wait for the next poll. Both are needed for the spec to observe the defect it guards.
- The keyed `Show`s take plain children. Solid 2.0's `Show` reads `props.children` inside its memo, and the compiled getter builds new components, so a new `when` mounts a fresh tree and viewer. The mutation check below confirms it.

Gaps:

- `make live` was not run (F2).

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                              |
| -- | -------- | ------------- | -------- | ------ | ------------------------------------------------------------------ |
| F1 | Medium   | Requirements  | Fix      | Open   | Re-selecting the selected session remounts the file tree and viewer |
| F2 | Low      | Testing       | Fix      | Open   | `make live` not run on a changed live assertion                    |
| F3 | Low      | Documentation | Fix      | Open   | `session/created` example lacks `directory`                        |
| F4 | Low      | Conventions   | Rejected | —      | Invalid-JSON file params answer with the selector message           |

## F1. Re-selecting the selected session remounts the file tree and viewer

Medium · Requirements · Fix · `cmd/ox/web/src/App.tsx:36`

The file tree and viewer `<Show>`s are keyed by `selected()`, which compares by object identity. `select` and the `hashchange` handler set a new `Selection` object every time, even when it names the session or draft already selected. Clicking the selected session in the sidebar therefore unmounts and remounts the tree and the viewer. The open file's text blanks until the next read, its scroll position resets to the top, and each expanded folder lists again from empty.

On the base, the views were keyed by the workspace ID string, so re-selecting the current session left them mounted. On a phone, the usual way back from the sessions drawer is to tap the current session. That tap now loses the reader's place in the file, which breaks what "the pane keeps its scroll position" protects today.

### Evidence

A temporary e2e probe opened a 500-line file in a session, set the pane's `scrollTop` to 2000, tagged the pane and the tree's DOM nodes, and clicked the selected session. Afterwards both tags were gone and `scrollTop` was 0: `{"before":2000,"after":{"top":0}}`. The probe was removed. On the base, by the source, the keyed `Show` saw the same workspace ID string, and the inner `Show`s saw the same truthiness and path, so nothing remounted.

### Action

Fix it at the signal. Give `selected`'s `createSignal` an `equals` that treats two selections as equal when they have the same `kind` and the same `id` or `workspaceId`. Then an equal selection notifies no reader, and the keyed `Show`s remount only when the selection really changes. A draft that becomes its session is a real change and still remounts.

Test: extend the e2e spec "the pane keeps its scroll position across a re-read and a click on the open file" in `workspace.spec.ts`. After its existing checks, click the selected entry in the sidebar again and check that `scrollTop` and `scrollLeft` stay at 2000 and 300 after the next `workspace/read_file` answer.

## F2. `make live` not run on a changed live assertion

Low · Testing · Fix · `internal/server/live_test.go:806`

The change edits `TestLive`'s child check to read `directory` from the `session/created` entry instead of `child.directory`. It also changes where the directory Claude runs in comes from: the stored field instead of the derived child-else-workspace value. `AGENTS.md` ("Verification") requires `make live` when a change alters how Ox drives Claude Code. Only `TestLive` runs the edited assertion against a real Claude child. The implementer reported it as not run and named nothing that blocks it.

### Evidence

`internal/server/live_test.go:806-818` in the diff. The implementer's report lists `make live` as not run, with no blocker.

### Action

Run `make live` on the final tree and report its result in the pull request. No new test.

## F3. `session/created` example lacks `directory`

Low · Documentation · Fix · `docs/protocol.md:592`

The notifications table now lists `directory` among `session/created`'s params, and the `workspace/list` example was updated to carry it. The `session/created` JSON example below the table still omits it, so the example contradicts the table and the code.

### Evidence

`docs/protocol.md:584` lists `directory`, and `:592` is `{"session_id":"session-id","workspace_id":"workspace-id","label":"session-","harness":"claude","created_at":"…","state":"parked"}`. `publishLocked` always sends `directory`.

### Action

Add `"directory":"/Users/kyle/src/ox"` to the example, between `created_at` and `state` as in the `workspace/list` example, then run `make docs`.

## F4. Invalid-JSON file params answer with the selector message

Low · Conventions · Rejected · `internal/server/methods.go:259`

`listFolder` and `readFile` answer a params object that does not decode, such as `"path": 5`, with `-32602` `exactly one of session_id and workspace_id is required`. That message can name the wrong field.

### Evidence

`methods.go:259` and `:287`.

### Action

Rejected. Every other method in `methods.go` folds a decode failure into its required-field message (`:113`, `:193`, `:317`, `:413`, and the rest), and the page always sends well-formed params. A separate message here would add a branch for a case no client produces.

## Checks run

- `go test ./internal/store/ ./internal/server/` on the final tree: pass.
- Spot-checked removals, each restored afterwards:
  - Removing `AddSession`'s absolute-directory check fails `TestChildren`.
  - Removing the upgrade's child-directory backfill fails `TestUpgradeAddsSessionDirectory`.
  - Making `fileRoot` return the session's workspace instead of its directory fails `TestCreateSession`.
  - Accepting both selectors in `fileRoot` fails `TestWorkspaceFiles`.
  - Keying the tree and viewer by `files()?.directory` instead of `selected()` fails the e2e spec "each session keeps its own expanded folders" at its `session not found` check.
- Temporary e2e probe for F1, removed afterwards.
- The `oldSchema` in `store_test.go` matches the base `schema` with `IF NOT EXISTS` removed.
- Taken from the implementer's report: `make test` passed; `make e2e` passed (165 passed, 4 live skipped); a headless Chrome check through `make dev` passed, including the upgrade of a seeded old-schema database. `make live` was not run (F2).

## Verification 1

Checked the uncommitted tree on base `5d72dd5` after `/ox-work fix`. Only `cmd/ox/web/src/App.tsx`, `cmd/ox/web/e2e/workspace.spec.ts`, and `docs/protocol.md` changed since the review.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | The `selected` signal takes `equals: sameSelection`, which compares `kind` and the session's `id` or the draft's `workspaceId`. The spec "the pane keeps its scroll position across a re-read, a click on the open file, and a click on the selected draft" clicks the selected draft again and checks the scroll position. With the signal back on identity equality, the spec fails at `workspace.spec.ts:944` (`scrollTop` expected 2000, received 0). With the fix it passes, as do "each session keeps its own expanded folders" and "each session and draft keeps its open file, and a draft's carries to its session". |
| F2 | Resolved | The implementer ran `make live`: `TestLive` and `TestLiveUr` passed. One approval spec failed once on open bug #6 and passed on a rerun. |
| F3 | Resolved | The `session/created` example at `docs/protocol.md:592` carries `"directory":"/Users/kyle/src/ox"`. `dprint check` passes. |

No new findings. The new `equals` only drops notifications for a selection equal to the current one. A draft that becomes its session is still a different selection, and every reader of `selected()` derives from its value.

Checks: the F1 mutation and three e2e specs above, and `dprint check`, run by this review. `make test`, `make e2e`, and `make live` as the implementer reports them.

## Verdict

`Ready to commit`: F1, F2, and F3 are resolved, and F4 is rejected.
