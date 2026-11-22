# Review of the web client test rewrite

## Scope and coverage

Reviewed the uncommitted changes on base `563d82e` against
[the plan](../plans/2026-11-22-004-web-tests-best-practice.md):

- the markup in `ApprovalRequest.tsx`, `FileTree.tsx`, `NewSessionView.tsx`,
  `SessionView.tsx`, `SettingsFields.tsx`, and `Sidebar.tsx`, and the
  `style.css` selectors that follow it
- the fixtures in `e2e/ui.ts` and `e2e/services.ts`, and
  `playwright.config.ts`
- `session.spec.ts`, `workspace.spec.ts`, and `approval.spec.ts`
- `stack.test.mjs`, the new `highlight.test.mjs`, and the `Makefile`
- `cmd/ox/web/AGENTS.md` and `docs/todo.md`

The tree had no other changes. `docs/todo.md` only loses this change's entry.
The implementing session's report, its proof script output, and its transcript
supplied the Tests proved and the Checks.

Judgments the coordinating session asked for:

- **Fault injections in place of a base-revision run.** They are enough. Every
  spec fails at the new `ox` fixture on the base, so a base run would prove
  nothing. Eleven source breaks covered each replaced probe: IME, cut send,
  lost creation, retried creation, background configurable, menu replacement,
  clock while hidden, clock while disconnected, pane remount per read, click
  reopening the file, and following. Three of the first breaks failed the build
  instead of a test. The implementer rewrote those three as valid breaks and
  reran them, and each then failed its test. The cut-send test first passed
  with its behavior broken. It was reordered to wait for the reconnect, and it
  then failed. The three `stack.test.mjs` rewrites and the highlight cases were
  proved the same way, and the highlight test gained `.sh` when a break went
  uncaught.
- **Open choices.** These are sound: the local `clocked` fixture; Request
  objects in place of `rpcCalls` for polls, since two root listings share their
  params; `answered()` in place of `waitForResponse`, since the response is
  only the relay's acknowledgement; `tapBeside`; `includeHidden` separators;
  and the extra bounds and tab assertions. The one that is not sound is the
  same-text re-render gap. See F1.
- **Markup leaves the page unchanged.** It does. The screenshot spec compared
  nine views at zero differing pixels. Those views included the Pending section
  with a prompt and a message, an approval card with its input expanded, a
  question form, the session list with a three-level family and its menu, the
  add and delete dialogs, the Files panel, and the narrow layout. By reading
  the CSS: `.queue` matches the transcript's flex column and `0.75rem` gap, and
  the bubbles' `align-self` and `max-width` resolve against the same width. The
  `h2`s reset font and weight, and `* { margin: 0 }` covers their margins.
  `nav header` matches only the workspace header row. Hover and `pointer:
  coarse` states were not screenshotted, but their selectors map one to one.
- **CSS values and fixed sleeps.** None remain. The remaining geometry is the
  behavior the plan keeps: the drag clamps of 160 and 640 px from `Panels.ts`,
  the 240 px split minimum, toggle boxes kept across headers, widths compared
  with first load, and `toBeInViewport`. `page.clock.runFor` advances fake
  time. `sleep 60` is a Bash input string.
- **The live ExitPlanMode failure.** The implementer attributed it correctly.
  The failure context shows Haiku writing its plan with `Write` to
  `~/.claude/plans/…` and then calling `ExitPlanMode {}`. The approval card
  showed both list items, and the tool call had no plan. That is **A plan
  written to a file shows no plan in its tool call**, and what it waits on has
  not changed, so it is not reported again. The old test made the same
  assertion. The plan file this run left behind is F2.

Gaps:

- Text selection and hover states were not screenshotted.

## Summary

| ID | Severity | Category      | Action | Status   | Title                                                                  |
| -- | -------- | ------------- | ------ | -------- | ---------------------------------------------------------------------- |
| F1 | Medium   | Testing       | Fix    | Resolved | No test catches the file pane re-rendering unchanged text              |
| F2 | Medium   | Security      | Fix    | Resolved | Live ExitPlanMode tests leave plan files in the user's ~/.claude/plans |
| F3 | Low      | Documentation | Fix    | Resolved | TestLive's doc comment now heads keepPlansIn                           |

## F1. No test catches the file pane re-rendering unchanged text

Medium · Testing · Fix · `cmd/ox/web/e2e/workspace.spec.ts:783`

The old spec marked a highlighted token in the pane with `dataset.marked`. It
then checked that the marker survived a re-read of the unchanged file, which
guarded "its pane is not re-rendered while the file is unchanged". The plan
deleted that probe and relied on the scroll position kept across a re-read.
Scroll position survives a same-text re-render, so nothing guards it now.

The user can see the difference. Re-setting the pane's HTML clears any text
selected in it, so with the regression a user selecting code to copy loses the
selection on every one-second read. The implementer reported the gap and left
it open.

### Evidence

- In headless Chrome, `pre.innerHTML = pre.innerHTML` on a selected `<pre>`
  turns `getSelection().toString()` from `"package main\n// note"` to `""`.
- I changed `FileView`'s `html` memo to re-emit on every read (`equals: false`
  and a read of `read()`). A scratch spec selected the pane's text in a `.go`
  file, ran three re-reads under the clock, and found the selection empty, so
  it failed. With the source unchanged, it passed. With the same break, "the
  pane keeps its scroll position across a re-read and a click on the open
  file" still passed.

### Action

Add an e2e test to `workspace.spec.ts`, under `clocked`: "a re-read of an
unchanged file keeps the text selected in the pane". It opens a small file of a
registered language, selects the pane's text with `selectText()`, and runs
`answered(page, "workspace/read_file", path)`. It then asserts the selection
still holds with `expect.poll(() => page.evaluate(() =>
getSelection()?.toString()))`. Playwright has no web-first assertion for a
selection. Prove that it fails when the `html` memo re-emits on every read, and
passes without that break.

## F2. Live ExitPlanMode tests leave plan files in the user's ~/.claude/plans

Medium · Security · Fix · `internal/server/live_test.go:546`,
`cmd/ox/web/e2e/approval.spec.ts:42`

Two live tests make Claude Code write a plan file into the user's own
`~/.claude/plans/`, and nothing removes it. One is the "own mode change"
section of `TestLive`. The other is "an ExitPlanMode plan renders as
Markdown". A test thereby touches files outside Ox's own and temporary
directories. Each `make live` adds files, and 58 had piled up since
2026-11-19. The coordinating session deleted them. The user asked for this to
be fixed in this change. The defect predates the rewrite.

### Evidence

- The implementing session's final `make live` run left
  `~/.claude/plans/load-and-call-the-modular-sonnet.md`, and its earlier runs
  left five `load-and-call-*.md` files, which it removed by hand.
- The coordinating session probed Claude Code 2.1.330 live with Haiku. With
  `plansDirectory` set in a workspace's `.claude/settings.json` (a path
  "relative to project root"), the plan file lands under that workspace.
  Without the setting, it lands in `~/.claude/plans/`.

### Action

Make both live tests write their plan files where the test cleans them up. A
`make live` run must leave no new file in `~/.claude/plans/`, and the live
tests must still pass. The e2e workspace from `services.ts` must stay empty
for the file tree specs, so the non-live specs must see no added file. The
design is `/ox-work`'s to choose. The check is a `make live` run, with
`~/.claude/plans/` listed before and after.

## F3. TestLive's doc comment now heads keepPlansIn

Low · Documentation · Fix · `internal/server/live_test.go:223`

F2's fix inserted `keepPlansIn` between TestLive's doc comment and
`func TestLive`. Go reads both comment paragraphs as `keepPlansIn`'s doc. That
leaves TestLive without one, and `keepPlansIn` with a doc that starts by
describing TestLive.

### Evidence

`live_test.go:223–226` has TestLive's two lines and then `keepPlansIn`'s two
lines in one comment block above `func keepPlansIn`. `func TestLive` at line
238 has no comment.

### Action

Move `keepPlansIn` and its comment out from under TestLive's comment, above it
or after `TestLive`, so each function has its own doc comment. No test applies.

## Checks run

- Read the implementer's proof output. All eleven source breaks failed their
  tests on the final runs. The first round's three build failures and the
  passing cut-send break were redone, and those runs failed their tests.
- Spot check: removed the menu's `ResizeObserver`. "a menu opened near the
  bottom edge stays on screen as it grows" failed at its second
  `toBeInViewport`.
- Spot check: set every session row to `aria-level={1}`. "children list
  beneath their parent…" and "a child archived alone hides beneath its parent…"
  failed.
- Probe for F1: a scratch selection spec passed on the unchanged source and
  failed with the `html` memo re-emitting on every read. The scroll test passed
  under the same break.
- Each break and the scratch spec were reverted, `cmd/ox/web/dist` was rebuilt
  from the restored source, and the tree matches the reviewed state.
- From the implementer's report, on the final tree: `make test` passed, with
  lint, Go, `stack.test.mjs`, and `highlight.test.mjs`. `make e2e` passed 107,
  with 4 live tests skipped, three times. Headless Chrome through `make dev` on
  ports 9080 and 9081 passed, including a Haiku prompt. In `make live`, the Go
  tests passed. `approval.spec.ts` failed once, on the known issue, and a rerun
  passed 4 of 4.

## Verification 1

Checked the working tree on base `563d82e`, with the fixes in
`workspace.spec.ts`, `approval.spec.ts`, and `internal/server/live_test.go`.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | "a re-read of an unchanged file keeps the text selected in the pane" failed when I made the `html` memo re-emit on every read: the selection went from the file's text to `""`. With the source restored, it passed. |
| F2 | Resolved | TestLive writes `{"plansDirectory": ".claude/plans"}` into its `t.TempDir()` workspace, where every section creates its sessions. Each live spec writes the same file into its own `services.workspace`, so no non-live spec sees it. `~/.claude/plans/` holds nothing newer than 2026-11-11, so the implementer's `make live` run added no file. |

New finding: F3.

Checks, from the implementer's report: `make test` passed. `make e2e` passed
108, with 4 live tests skipped. `make live` passed: TestLive, TestLiveUr, and
`approval.spec.ts` 4 of 4. The fixes change only tests, so the headless Chrome
check does not apply. I reran the new F1 test with and without the break,
then restored the source and rebuilt `dist`.

## Verification 2

Checked the working tree on base `563d82e`, after F3's fix in
`internal/server/live_test.go`.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F3 | Resolved | `keepPlansIn` and its doc comment now sit above TestLive's doc comment, and that comment directly precedes `func TestLive`. |

No new findings. The fix moves a comment only, so no test applies. Checks:
`make test` passed, per the implementer's report. The `make e2e` and
`make live` runs from Verification 1 still cover the tree, because nothing
they run has changed since.

## Verdict

Ready to commit. F1, F2, and F3 are resolved.
