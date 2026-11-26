# Review: rewrite the stylesheet on design tokens

## Scope and coverage

Base revision `4452e91` (`git merge-base origin/main HEAD`); HEAD `02316d5` holds the already-reviewed tab layout. Reviewed the uncommitted changes on `tabs-and-splits`: `cmd/ox/web/src/style.css`, the button and text-field markup in `App.tsx`, `ApprovalRequest.tsx`, `SessionView.tsx`, and `Sidebar.tsx`, the new `cmd/ox/web/style.test.mjs`, and its line in `Makefile`, against `agents/plans/2026-11-26-tabs-and-splits-style.md`. The parity spec and its baseline snapshots are uncommitted scaffolding and were rerun, not reviewed as code. The plan's `cmd/ox/web/AGENTS.md` edit is deferred by the parent session and was not expected.

Gaps:

- The parity capture leaves out the usage popover, the question form's Decline and Previous buttons, the Reload and Reopen draft alerts, and the archived composer's Unarchive button. For the buttons, the review compared the old default `button` rule with `button.outline` declaration by declaration instead. The two set the same values.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                                           |
| -- | -------- | ------------ | -------- | ------ | --------------------------------------------------------------- |
| F1 | Low      | Construction | Fix      | Resolved | Session tree guides use spacing tokens for the status dot's geometry |
| F2 | Low      | Simplicity   | Fix      | Resolved | The background status dot repeats the reset's `box-sizing`      |
| F3 | Low      | Simplicity   | Rejected | —      | Truncation declarations repeat across sections                  |
| F4 | Low      | Intent       | Rejected | —      | The sidebar chevron keeps a `:hover { background: transparent }` |

## F1. Session tree guides use spacing tokens for the status dot's geometry

Low · Construction · Fix · Resolved · `cmd/ox/web/src/style.css:384-396`

The child-session guide is placed with `--space-4` and `--space-1`, but those lengths are really the status dot's position and radius:

- `left` and `width` use `var(--space-4)` for "the dot's center, 1rem in". That is `var(--space-2) + var(--icon-size) / 2` (the row's padding plus half the status box). The guide's width up to the child's dot is `var(--session-indent) - var(--status-dot) / 2`.
- `nav li.child.first::before`'s `+ var(--space-1)` is the parent dot's radius, `var(--status-dot) / 2`.

The values match today only because the numbers happen to line up. If someone changes `--icon-size`, `--status-dot`, or `--session-indent`, the guides drift away from the dots. The comment on line 384 also gives the value directly ("dots sit 1rem in"), when the plan says such comments should name the token.

### Evidence

Traced from the markup: `nav li` sets `padding-left: calc(var(--space-2) + var(--depth) * var(--session-indent))`, then `.status` is `--icon-size` wide with a `--status-dot` dot centered in it. So the parent's dot center sits at `0.5rem + 0.5rem = 1rem` past its own indent. The child's dot starts `1.25rem − 0.25rem = 1rem` past that, and the parent's dot bottom sits `0.25rem` below its center. Each of these matches `--space-4` and `--space-1` by coincidence.

### Action

Write `left`, `width`, and the `.first` `top` in terms of `--space-2`, `--icon-size`, `--status-dot`, `--session-indent`, and `--border-width`. Reword the comment to say the dots sit at the row's padding plus half the status box. The computed values stay the same. The parity capture's wide shots, which show "Back off sync retries" with its children, confirm that nothing moves. No new test: `style.test.mjs` cannot tell a coincidental token from the right one.

## F2. The background status dot repeats the reset's `box-sizing`

Low · Simplicity · Fix · Resolved · `cmd/ox/web/src/style.css:444`

`.status.background::before` sets `box-sizing: border-box`, which the reset's `*, *::before, *::after` rule already sets on every pseudo-element.

### Evidence

`style.css:2` applies `box-sizing: border-box` to `*::before`. No rule in between sets it to anything else.

### Action

Delete `box-sizing: border-box;` from `.status.background::before`. Parity confirms the change: the sidebar's dots appear in every wide shot.

## F3. Truncation declarations repeat across sections

Low · Simplicity · Rejected · `cmd/ox/web/src/style.css`

The set `min-width: 0; overflow: hidden; white-space: nowrap; text-overflow: ellipsis` appears in about ten rules: the tool detail, the model option, file-tree names, Changes folders, the branch, change titles, and others.

### Action

Rejected. Each copy sits in its own component's section, as the plan's file structure requires. The plan's shared-declaration rule, illustrated by the tab label rule, applies within a section. A single rule spanning components would need a utility class or a cross-section selector list, which would break "each element's rules live in its component's section". Keeping the copies costs less.

## F4. The sidebar chevron keeps a `:hover { background: transparent }`

Low · Intent · Rejected · `cmd/ox/web/src/style.css:433`

The plan says the button rewrite removes every `:hover { background: transparent }`. One remains, on `nav li button.chevron-right`.

### Action

Rejected. That plan sentence concerns rules that undid the old default button look. This rule overrides `button.icon:hover` on purpose: the chevron is a ghost icon button whose box is only its hit area, so hovering it should keep the row's color, as the comment above it says.

## Checks run

- Parity: `TMPDIR=/tmp OX_SCREENSHOTS=1 OX_PARITY=1 npx playwright test parity`. 32 of 34 shots match pixel for pixel. `model-picker-light` (431 px) and `model-picker-dark` (428 px) differ only in the effort slider's Low, High, and Max labels, which is the planned change from 10px to `--text-2xs`. The waiting dot's change to `--warning` produces no pixel difference.
- `node --test style.test.mjs`: 5 of 5 pass. Each check failed when its violation was injected into `style.css`, which was restored byte for byte afterwards: a `1.5rem` literal, a hex color, a literal `z-index`, an `!important`, a duplicate `.prompt` rule, an undefined `var(--nope)`, and an unused `:root` token.
- Node suites from `make test`, `style.test.mjs` included: 47 pass.
- `npm run lint`: passes. The 21 `noDescendingSpecificity` warnings in `style.css` (20 on the old file) follow from the plan's component order.
- `make e2e`: 216 passed, 6 skipped.
- A grep of every `<button>` without a class found only buttons with roles (menu item, tab, menu radio, panel toggle, hunk header) that have their own rules. No button relies on the old default look. Every class selector in the stylesheet matches a class the markup renders.

## Verification 1

Checked the uncommitted working tree against the stylesheet as first reviewed. Only the two fixes changed. Parity rerun: 32 of 34 shots match exactly, and `model-picker-light` (431 px) and `model-picker-dark` (428 px) differ only in the planned tick labels, the same counts as before the fixes. The Node suites, `style.test.mjs` included, pass 47 of 47. `npm run lint` passes. `make e2e` was not rerun: the fixes leave every computed value the same, and the parity rerun confirms it.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | The guide's `left` is `--space-2 + --icon-size / 2`, its `width` is `--session-indent - --status-dot / 2`, and the `.first` `top` adds `--status-dot / 2`. Each equals the old value. The comment no longer states a value. Parity shows no change. |
| F2 | Resolved | `box-sizing` is gone from `.status.background::before`. Parity shows no change. |

No new findings.

## Verdict

`Ready to commit`.
