# Review: render messages and child reports as Markdown

## Scope and coverage

Uncommitted changes on `message-markdown` against base `f08ea0d` (`git merge-base origin/main HEAD`, also `HEAD`): `cmd/ox/web/src/SessionView.tsx`, `cmd/ox/web/src/style.css`, `cmd/ox/web/e2e/session.spec.ts`, and `docs/functional-requirements.md`, checked against `agents/plans/2026-11-24-message-markdown.md`.

The requirements diff matches the plan's Requirement changes word for word for Messages in the transcript and Child reports. Rendering goes through the one `Markdown` component, so messages and reports keep the reply rules (escaped HTML, no images, safe links). Pending, taken, and reloaded messages all render through `PromptBubble`, so they render alike. The user's own prompts and the composer's submitted prompt stay plain text, as Prompts as sent requires.

Gaps:

- The CSS (`white-space: normal` on `.markdown`, code backgrounds inside a bubble) was checked by reading specificity, not in a browser; `cmd/ox/web/AGENTS.md` keeps styling out of Playwright assertions. The session view's message and report bubbles are for the user to judge.

## Summary

| ID | Severity | Category      | Action | Status   | Title                                         |
| -- | -------- | ------------- | ------ | -------- | --------------------------------------------- |
| F1 | Low      | Documentation | Fix    | Resolved | Requirements file fails the docs format check |
| F2 | Low      | Simplicity    | Fix    | Resolved | `MessageBubble` tests the same condition twice |

## F1. Requirements file fails the docs format check

Low · Documentation · Fix · Resolved · `docs/functional-requirements.md:202`

The Child reports edit leaves a short line (`as Markdown; a child`) without rewrapping the rest of the paragraph. `dprint check` reports the file as unformatted, so the pre-commit hook refuses the commit.

### Evidence

`npx --prefix cmd/ox/web dprint check docs/functional-requirements.md` reports one unformatted file, with the diff rewrapping lines 203 to 207.

### Action

Run `make docs`. The rewrap changes only line breaks, so the requirement text stays as the plan approved it.

## F2. `MessageBubble` tests the same condition twice

Low · Simplicity · Fix · Resolved · `cmd/ox/web/src/SessionView.tsx:262`

`MessageBubble` now has two adjacent `<Show when={props.input.from !== undefined}>` blocks: one for the sender label, one choosing `Markdown` over plain text. Both are one rule, a message versus a prompt, and they can drift apart.

### Evidence

`SessionView.tsx:262-267`.

### Action

Merge them into one `<Show when={props.input.from !== undefined} fallback={props.input.text}>` whose body is the `.from` label followed by `<Markdown text={props.input.text ?? ""} />`. The existing e2e tests "a child session shows its title, its directory, and its parent's message, also after a reload" and "your own prompt stays plain text" cover both branches.

## Checks run

- `make test`: passed.
- `make e2e`: 161 passed, 4 skipped.
- With `SessionView.tsx` reverted to the base: "a child session shows its title, its directory, and its parent's message, also after a reload" and "a child's reply reaches its parent as a report headed with its title, also after a reload" fail; "your own prompt stays plain text" passes, as expected for a guard of existing behavior that fails only if prompts start rendering as Markdown.
- `dprint check docs/functional-requirements.md`: fails (F1).
- `make live`: not required; the change does not alter how Ox drives Claude Code. Headless Chrome check through `make dev`: not required; the change does not alter how the page reaches `oxd`.

## Verification 1

Uncommitted tree on base `f08ea0d` after `/ox-work fix`. Checks: `dprint check` passed; `make test` passed; `make e2e` 161 passed, 4 skipped.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `dprint check` exits 0; the Child reports paragraph is rewrapped with its wording unchanged from the plan |
| F2 | Resolved | `MessageBubble` has one `<Show>` holding the label and `Markdown`, with the prompt's plain text as its fallback; the message and plain-prompt e2e tests pass |

No new findings. The fixes change only the requirements file's line breaks and the `MessageBubble` markup.

## Verdict

`Ready to commit`: F1 and F2 resolved.
