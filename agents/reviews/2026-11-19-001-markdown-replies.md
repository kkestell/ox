# Review: Render replies and the ExitPlanMode plan as Markdown

## Scope and coverage

Reviewed the uncommitted changes on `main` over base `80598b4` (the files in
`git status`: `Markdown.tsx` new; `SessionView.tsx`, `ApprovalRequest.tsx`,
`ox.tsx`, `style.css`, `package.json`, `package-lock.json`, the e2e specs and
`services.ts`, `docs/architecture.md`, `docs/todo.md` modified), against
`agents/plans/2026-11-19-001-markdown-replies.md`.

The change delivers the plan's Goal and keeps its Decisions: markdown-it 15
with `html: false`, default `validateLink`, `linkify`, images disabled, and a
`link_open` rule adding `target="_blank"` and `rel="noopener noreferrer"`;
replies and the `ExitPlanMode` plan (approval card and transcript tool card)
render through `Markdown`; prompts, tool input and results, errors, and
`exited` entries stay plain. No new state, request, or wait.

Gaps:

- No `make dev` headless Chrome check: the change does not alter how the page
  reaches `oxd`, so step 9 does not call for it.

## Summary

| ID | Severity | Category    | Action | Status | Title                                                  |
| -- | -------- | ----------- | ------ | ------ | ------------------------------------------------------ |
| F1 | Low      | Conventions | Fix    | Open   | New `.plan` selector adds the only Biome lint warning  |
| F2 | Low      | Simplicity  | Fix    | Open   | Lockfile drops unrelated `libc` fields                 |

## F1. New `.plan` selector adds the only Biome lint warning

Low · Conventions · Fix · `cmd/ox/web/src/style.css:177`

`npm run lint` (run by `make test`) reports
`lint/style/noDescendingSpecificity`: `.approval .plan` (0,2,0) at line 219
follows `details.tool .card .plan` (0,3,1) at line 177. The two selectors
never match the same element, since an approval card is never inside a tool
card, so nothing renders wrong. The warning still matters: lint was clean at
the base revision, and a standing warning in every `make test` run hides the
next one that is real.

### Evidence

`npm --prefix cmd/ox/web run lint` on the working tree: `Found 1 warning.` at
`src/style.css:219`. The same command at `80598b4`: no warnings. With line 177
changed to `.card > .plan { … }` (0,2,0), `biome check src/style.css` reports
nothing.

### Action

Change the selector on line 177 to `.card > .plan`; `.card` is used only by
the tool card. No test: lint is the check.

## F2. Lockfile drops unrelated `libc` fields

Low · Simplicity · Fix · `cmd/ox/web/package-lock.json:139-205`

Besides adding markdown-it and its dependencies, the lockfile diff deletes the
`libc` (`glibc`/`musl`) fields from the four Linux optional binary packages
near line 139. The base lockfile was written by an npm that records `libc`;
the local npm 10.9.4 does not, so `npm install` stripped them. The next
install with the newer npm adds them back, and the commit carries churn
unrelated to the change.

### Evidence

`git diff cmd/ox/web/package-lock.json`: four hunks each removing a
`"libc": [ "glibc" | "musl" ]` block, unrelated to the markdown-it entries.

### Action

Restore the four deleted `libc` blocks, keeping the markdown-it additions. No
test.

## Checks run

- `make test`: passes; lint reports the F1 warning.
- `make e2e`: 14 passed, 2 skipped (the live approval specs).
- `OX_LIVE=1 npm --prefix cmd/ox/web run e2e -- approval --repeat-each 3`:
  6 passed, including "an ExitPlanMode plan renders as Markdown" three times.
  `make live`'s Go `TestLive` was not run: the change does not alter how Ox
  drives Claude Code.
- New tests at base `80598b4` (detached worktree, removed afterwards): "a reply
  renders as Markdown and its prompt stays plain" fails at `.reply strong`;
  "an ExitPlanMode plan renders as Markdown" fails at `.approval .plan li`.
- Each added condition removed in turn, with the session or live spec failing
  on the matching assertion: `html: true` (`.reply b` count), image rule
  enabled (`.reply img` count), no `target` (`target` attribute), no `rel`
  (`rel` attribute), `linkify: false` (link `https://example.org`), plan
  removed from the tool card (`details.tool .plan li`), approval summary
  subject reverted (`summary .subject` text `Input`).

## Verification 1

Checked the working tree over base `80598b4`; since the review, only
`style.css` and `package-lock.json` changed. Ran `npm ci` (accepts the
lockfile), `make test` (lint clean, Go tests pass), `make e2e` (14 passed,
2 skipped live specs), and `OX_LIVE=1 npm --prefix cmd/ox/web run e2e --
approval` (2 passed, including "an ExitPlanMode plan renders as Markdown").

| ID | Status   | Evidence                                                                                          |
| -- | -------- | ------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `style.css:177` is `.card > .plan`; `npm run lint` reports no warnings                            |
| F2 | Resolved | `git diff package-lock.json` has no deletions; only the markdown-it entries are added; `npm ci` accepts it |

No new findings.

## Verdict

`Ready to commit`: F1 and F2 are resolved and every check passed.
