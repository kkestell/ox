# Show the PR inbox and Chat in ox’s right sidebar

- Task: `2026-11-13-003-github-pr-sidebar`
- Kind: plan
- Status: ready
- Source HEAD: `8cc9ae064bebb26a374e56517f663142961eff93`
- Dependencies: `2026-11-13-001-github-pr-feed`, `2026-11-13-002-github-pr-chat`

## Outcome and boundaries

The right-panel toggle reveals a global PR inbox. Display To fix, Replies, Ready to merge, To review, and Waiting on others, with counts, reasons and Chat. Chat selects the ordinary session from 002 and seeds its composer once with unsent PR context. No Review button, automatic prompt, merge controls, extra tabs, or visual redesign.

## Current system

`Workbench.tsx` owns the right toggle, sash and sizing, but its aside is empty. `App.tsx` owns selection and renders `Composer` keyed by session ID. `SessionList.tsx` and `styles.css` define sidebar groups/rows/actions. Native dialog/opener dependencies exist; `ui/e2e` uses the embedded WebDriver and isolated daemon.

## Design

Give Workbench a right-content slot, mounted while visible. Keep results and pending Chat operations in an App-owned `usePullRequests` hook so hiding the panel preserves results and busy state. Refresh on opening, window focus while shown, and Refresh. Coalesce concurrent requests; keep prior results during refresh. No interval poll or background daemon. Finish launched operations even if the panel hides.

Define TypeScript mirrors in `ui/src/github.ts`; separate hook, `PullRequests`, and repository dialog. Consume 001’s classifications without recomputing attention. Preserve group/native row order, omit empty groups, retain collapse choices during the app session, and count account interpretations separately. Show “Nothing needs you” only when successful data supports it. Show missing-CLI/login guidance, loading, per-account errors with last-success time, and truncation notes without replacing usable rows.

Extract shared sidebar layout, group-heading, and row primitives from `SessionList`; migrate existing callers. Generalize `.session` layout rules into a shared row class while keeping session-specific selectors/semantics distinct. Shared rules own height, padding, title truncation/fade, hover/focus actions, icon dimensions and action inset. Both pane headers use `pane-header`. Use existing tokens and `icon-button`, with lucide icons; no new palette or pixel offsets. PR titles open GitHub through the existing opener plugin; hover/focus shows only Chat. Tooltips/accessibility names include repository, number, account, author and reason. Native context menu provides Chat, Open on GitHub and Copy URL, with keyboard access; no Review item.

Chat disables that row until completion. On `needs_repository`, open `Dialog` with a clone-target field, directory chooser and Use Existing Folder action. Before launch, Cancel dismisses normally. Clone submission closes the dialog and starts the row operation, showing busy state; no cancellation is advertised after launch. Failures use existing `showError` and allow another attempt. A completed clone/workspace remains available after later failure, as 002 specifies.

On success, select the returned session through App’s existing selection flow. Keep its draft seed until the workspace watch lists it and Composer mounts; consume the seed once. `Composer` initializes from that seed and otherwise retains existing send/edit behavior. Switching sessions must never reinsert context after it was consumed/sent. No general draft-persistence subsystem.

## Check your assumptions

1. **Kept: refresh on opening/focus/manual request, not every minute.** A focused open inbox waits for Refresh. Periodic updates would add continuous GitHub work.
2. **Kept: PRs directly in the sidebar.** Ox has no other right-panel tools needing tabs.
3. **Kept: launched operations continue without Cancel.** Busy state survives hiding/reopening; cancellation across clone/session creation is excluded.

## Implementation sequence

1. Extract sidebar primitives/shared CSS and migrate `SessionList`, updating E2E selectors only where shared classes change.
2. Add the typed hook, PR rows/groups/errors and Workbench right slot; wire refresh events and native menu/opener behavior.
3. Add repository dialog, Chat completion/selection, and one-time Composer seeding.
4. Add isolated CLI fixtures and E2E acceptance cases; compare both sidebars visually.

## Verification

- `ui/e2e/fixtures` supplies `gh` via the app launcher’s PATH and private fixture files. Use a minimal scripted `claude` handshake, following `crates/claude-code-acp/tests/protocol.rs`, for UI-created sessions. No production test flags/modes or real GitHub/model requests.
- `pull-requests.e2e.ts`: global inbox without workspaces; all groups/counts; keyboard/hover actions; no Review; open/focus/manual refresh; retained rows/errors; same PR under two accounts; collapse and resize behavior.
- Chat cases: existing repository, add existing, clone, fork head, failure/retry, disabled duplicate action across panel hide, selected new worktree, unsent editable context consumed once, unchanged other checkout. Use fixture Git URL rewrites and assert filesystem commits, not only UI labels.
- Re-run existing session/worktree/archive cases after shared-row extraction. For fixes, temporarily undo the fix and confirm its regression test fails.
- Commands: `make check-rust`, `make check-ui`, `make e2e`, `make smoke-claude`. Reuse unchanged fast-check results; run both slow checks over the whole task. Planning runs no code checks.
- Manual: compare header centers, group/row sizes, colors, icons and action alignment across both sidebars at narrow/wide sizes, hover and keyboard focus.
