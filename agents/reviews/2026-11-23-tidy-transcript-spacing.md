# Review: tidy transcript spacing

## Scope and coverage

Uncommitted changes on `tidy-transcript-spacing` over base `e332d95` (HEAD and merge base): `cmd/ox/web/index.html`, `src/App.tsx`, `src/Panels.tsx`, `src/SessionView.tsx`, `src/style.css`, `e2e/session.spec.ts`, `e2e/workspace.spec.ts`, and `docs/functional-requirements.md`. There is no plan. The user directed each change in the requesting session, so the brief from that session and the requirement text the user approved there are the intent. The user judges how the views look; this review covers behavior, tests, and conformance to `AGENTS.md` and the requirements.

Open `bug` issues #5, #6, #33, and #34 concern the daemon and Claude harness, which the change does not touch.

Gaps:

- iOS focus zoom and Android pinch zoom cannot be observed in headless Chrome. The `maximum-scale=1` behavior was judged from the platforms' documented handling of the viewport meta tag.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                                           |
| -- | -------- | ------------ | -------- | ------ | --------------------------------------------------------------- |
| F1 | Low      | Requirements | Fix      | Open   | Opening a tool call stops the transcript following new output   |
| F2 | Low      | Simplicity   | Fix      | Open   | The page error decides reconnecting versus closed twice         |
| F3 | Low      | Correctness  | Rejected | —      | `maximum-scale=1` turns off pinch zoom in Chrome on Android     |
| F4 | Low      | Testing      | Rejected | —      | No test that focusing a 15px field does not zoom the page       |

## F1. Opening a tool call stops the transcript following new output

Low · Requirements · Fix · `cmd/ox/web/src/SessionView.tsx:30`, `docs/functional-requirements.md` ("Following output")

The transcript follows new output only while its scroll offset is zero, as the `column-reverse` comment in `style.css` says. `keepRowInPlace` changes the offset by the height the row gains or loses. Opening a tool call while the transcript follows the bottom moves the offset away from zero. The latest output drops below the screen, and new output stops showing until the user scrolls back down, though the user never scrolled up. "Following output" says the transcript follows new output "unless the user has scrolled up", so the new "Tool calls" text and "Following output" contradict each other.

### Evidence

From the source: `.log` is `column-reverse` and nothing else resets its scroll offset, so a nonzero offset means not following. `keepRowInPlace` sets `scrollTop += delta`, and opening a row adds height, so `delta` is nonzero. The new e2e test shows the offset moving: without the handler the summary ends at y=325, and with it the summary stays at y=532.

### Action

The user chose to keep the behavior and amend the requirement: opening a tool call works like scrolling up, and following resumes at the bottom. Change "Following output" to text the user approves. Proposed:

> **Following output.** The transcript follows new output while it is scrolled to its bottom.

No code change and no new test. `opening and closing a tool call keeps its summary in place` already guards the scroll.

## F2. The page error decides reconnecting versus closed twice

Low · Simplicity · Fix · `cmd/ox/web/src/App.tsx:154`

The page error has three cases: a reconnecting stream, which shows the "Reconnecting…" status; a closed stream, which shows the alert with Reload; and any other error, which shows the alert. The outer `<Show>` tests `!(error() instanceof Disconnected) || (error() as Disconnected).closed`. The inner `<Show>` then tests `error() instanceof Disconnected && (error() as Disconnected).closed` again to decide on Reload. Both conditions use casts. A change to one case has to keep both tests in agreement.

### Evidence

From the source, `App.tsx:154-170`.

### Action

Render the three cases as one `<Switch>` with a `<Match>` for each. Derive each case once, for example a memo that narrows `ox.error()` to `{ kind: "reconnecting" } | { kind: "closed"; message } | { kind: "error"; message }`, or `Match` on `instanceof Disconnected` with the narrowed value. Keep the existing tests: `a reconnecting stream shows as a status, not an error` and `a closed stream offers a Reload button that reconnects` cover all three cases between them.

## F3. `maximum-scale=1` turns off pinch zoom in Chrome on Android

Low · Correctness · Rejected · `cmd/ox/web/index.html:5`

Chrome and Samsung Internet on Android honor `maximum-scale=1` and stop pinch zoom unless the user turns on the accessibility setting that forces it. Safari on iOS ignores the limit for pinch zoom and only stops focus zoom, which is the intent.

Rejected: no requirement covers pinch zoom, Ox targets the iPhone Home Screen app, and the user chose 15px fields with this tag over 16px fields.

## F4. No test that focusing a 15px field does not zoom the page

Low · Testing · Rejected · `cmd/ox/web/src/style.css:463`

"Touch screens" requires that focusing a text field not zoom the page, and the change moves that guarantee from 16px fields to the viewport tag. No test covers it.

Rejected: headless Chrome does not focus-zoom, so no e2e test can observe it, and a test that reads the meta tag checks the implementation, not the behavior.

## Checks run

- `make test`: passed (Biome, `tsc`, Go tests, 23 Node tests).
- `make e2e`: reported passing by the requesting session, 137 tests, on this tree. Re-ran the four new or changed tests here (`reasoning shows in the transcript…`, `opening and closing a tool call keeps its summary in place`, `a reconnecting stream shows as a status, not an error`, `a reload hides the file tree and restores the default widths`): all passed.
- Removed the handler from `.log`: `opening and closing a tool call keeps its summary in place` fails (expected 532, received 325).
- Made the status fallback unreachable in `App.tsx`: `a reconnecting stream shows as a status, not an error` fails.
- Started the file tree shown in `Panels.tsx`: `a reload hides the file tree and restores the default widths` fails.
- Every removal was reverted. The diff stat matches the reviewed tree.
- `make live`: not required. The change does not alter how Ox drives Claude Code.
- `make dev` in headless Chrome: not required. The change does not alter how the page reaches `oxd`.

## Verification 1

Checked the uncommitted tree on `tidy-transcript-spacing` over `e332d95`, after the fixes and the rewrite of the summary test to role and text locators. It finds the summary with `getByText("Bash", { exact: true })` and the body with `getByText('"f": 6')`, and asserts the position with `expect.poll`.

| ID | Status   | Evidence                                                                                                                                                                                            |
| -- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | "Following output" reads "The transcript follows new output while it is scrolled to its bottom.", the wording the user approved. `opening and closing a tool call keeps its summary in place` fails with the handler removed. |
| F2 | Resolved | `App.tsx` renders the page error as one `Switch`: a reconnecting `Match`, a closed `Match` with Reload, and a fallback alert. `a closed stream offers a Reload button that reconnects` fails with the closed `Match` disabled. |

No new findings.

Checks run:

- `make test`: passed.
- `make e2e`: 137 passed, 4 skipped. The first two runs saw 6 failures in total, each a missing trace file under `test-results/`. The other session was running e2e in the same checkout at the time. A run with no other e2e process going passed.
- Removed the handler from `.log`: the rewritten summary test fails. Disabled the closed-stream `Match`: `a closed stream offers a Reload button that reconnects` fails. Both edits were reverted.

## Verdict

`Ready to commit`. F1 and F2 are resolved.
