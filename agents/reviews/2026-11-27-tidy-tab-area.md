# Tidy TabArea, tabs.ts, and the composer box

## Scope and coverage

Uncommitted changes on `tidy-tab-area` against base `74f52dd`, checked against
`agents/plans/2026-11-27-tidy-tab-area.md` (issue #207), with the parent's
correction that `keepPromptFocus` stays exported from `Composer.tsx` because
`SessionView.tsx` uses it on the Jump to bottom button. Files:
`tabs.ts`, `TabArea.tsx`, `SessionView.tsx`, `NewSessionView.tsx`, and the new
`Composer.tsx`. Web client only; no state owner, protocol, persistence, or
harness driving is touched, so no concern reviewers were started.

Gaps:

- `make e2e` was not re-run here; the implementing session ran it.

## Summary

| ID | Severity | Category     | Action | Status | Title                                 |
| -- | -------- | ------------ | ------ | ------ | ------------------------------------- |
| F1 | Low      | Construction | Fix    | Resolved | `setMode` adds a cast the plan removes |

## F1. `setMode` adds a cast the plan removes

Low · Construction · Fix · Resolved · `cmd/ox/web/src/tabs.ts:229`

The plan's goal is removing casts from the tab code, and the change adds one:
`setMode` passes `(t) => ({ ...t, mode }) as Tab` to `mapTab`. The callback
receives any `Tab`, and the cast hides that it would put `mode` on a non-file
tab. It cannot today, because the key `file:${path}` only matches a file tab,
but the type no longer says so.

### Evidence

Removing `as Tab` still typechecks (`tsc --noEmit` clean), so the cast does no
work. The pre-change code narrowed with `t.kind === "file"`.

### Action

Narrow in the callback instead of casting:
`(t) => (t.kind === "file" ? { ...t, mode } : t)`. No new test; the existing
`setMode keeps the mode on the tab through moves` in `tabs.test.mjs` covers it.

## Notes on the rest

- `mapTab` matches by `tabKey`, which is `file:<path>` for files and the kind
  otherwise, so `setMode` and `reveal` match exactly the tabs they matched
  before.
- `PromptBox` renders the same form both views rendered: same attributes,
  handlers, and `required` rule. `autofocus` is now `{props.autofocus}`, which
  is `true` in the new-session view and absent in the session view, as before.
- `focusedGroup` and `focusedTab` remove both casts from `TabArea.tsx`;
  `GroupView`'s accessors read `active()` as the IIFEs did.
- `tabs.test.mjs` has no unit case for `reveal` or `focusedTab`, contrary to
  the plan's test plan; `e2e/workspace.spec.ts` drives reveal from the Changes
  tab, and the refactor adds no behavior, so no test is required.

## Checks run

- `tsc --noEmit`: clean.
- `node --test tabs.test.mjs`: passes.
- `biome check src`: no diagnostics in the changed files.
- Spot check: removing the `as Tab` cast in `setMode` still typechecks
  (reverted).
- `make test` and `make e2e`: reported run by the implementing session.

## Verification 1

Working tree on base `74f52dd`, after the F1 fix. Checks: `tsc --noEmit`
clean, `node --test tabs.test.mjs` 16 passed; `make test` reported passing by
the implementing session.

| ID | Status   | Evidence                                                                                                   |
| -- | -------- | ---------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `setMode` narrows with `t.kind === "file"` and carries no cast; `setMode keeps the mode on the tab through moves` passes |

No new findings.

## Verdict

`Ready to commit`.
