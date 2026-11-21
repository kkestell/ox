# Todo

Planned work, ordered by priority: unfixed audit findings, then gaps in
features Ox already supports. Each finding links its section in the audit,
which holds the evidence and the correction.

## Defects

- [ ] **Reply text lost on an abrupt exit.** Text shown live is not stored
  until the next non-text update, so a killed `oxd` loses it. Bound the
  unstored text, or reword **Surviving restarts**; the user decides.
  [Deslop F1](../agents/audits/2026-11-20-002-deslop.md#f1-reply-text-shown-live-is-lost-on-an-abrupt-oxd-exit)
- [ ] **One Ctrl-C forces `oxd` past graceful shutdown under `make dev`.**
  The terminal's `SIGINT` and the stack's `SIGTERM` both reach `oxd`.
  [Deslop F2](../agents/audits/2026-11-20-002-deslop.md#f2-one-ctrl-c-under-make-dev-forces-oxd-past-graceful-shutdown)
- [ ] **`create_session` publishes the child before it owns its first
  message.** An archive or shutdown in between deletes or strands the child.
  [Deslop F3](../agents/audits/2026-11-20-002-deslop.md#f3-create_session-publishes-the-child-before-its-first-message-has-an-owner)
- [ ] **Archive and unarchive act on a stale session.** Unarchive can flip a
  recreated session's stored flag, and archive replies before its members
  have stopped.
  [Deslop F4](../agents/audits/2026-11-20-002-deslop.md#f4-archive-and-unarchive-act-outside-the-owner-of-session-membership)
- [ ] **Enter on the new-session effort slider sends the draft.**
  [Deslop F5](../agents/audits/2026-11-20-002-deslop.md#f5-enter-on-the-new-session-effort-slider-creates-the-session-and-sends-the-draft)
- [ ] **A second `oxd` can share the database.** Only the socket is
  exclusive, so a daemon on another socket repairs and archives the first
  one's sessions.
  [Deslop F6](../agents/audits/2026-11-20-002-deslop.md#f6-only-the-socket-not-the-database-has-one-owner)
- [ ] **A race report from a stopping `oxd` is lost.** e2e and dev restarts
  ignore the exit status of the daemon they stop.
  [Deslop F7](../agents/audits/2026-11-20-002-deslop.md#f7-a-race-report-from-a-stopping-oxd-is-lost-in-e2e-and-dev-restarts)
- [ ] **Tests that pass when their behavior breaks.** Six tests, from the
  settings passed to the next start to `TestLive`'s ordering.
  [Deslop F8](../agents/audits/2026-11-20-002-deslop.md#f8-tests-that-pass-when-the-behavior-they-name-breaks)
- [ ] **The startup timeout drops Claude's stated reason.**
  [Deslop F9](../agents/audits/2026-11-20-002-deslop.md#f9-claude-startup-and-configuration-edge-outcomes),
  first item.
- [ ] **A send clears retyped text equal to the sent prompt.**
  [Deslop F10](../agents/audits/2026-11-20-002-deslop.md#f10-a-successful-send-clears-retyped-text-equal-to-the-sent-prompt)
- [ ] **Tool calls with only structured input show a blank summary.**
  [Deslop F13](../agents/audits/2026-11-20-002-deslop.md#f13-tool-calls-with-only-structured-input-show-a-blank-summary)
- [ ] **A failed file request leaves the pane blank with no error.**
  [Deslop F14](../agents/audits/2026-11-20-002-deslop.md#f14-a-failed-file-post-leaves-the-pane-blank-or-stale-with-no-error)
- [ ] **An open sidebar menu grows past the viewport.**
  [Deslop F15](../agents/audits/2026-11-20-002-deslop.md#f15-an-open-sidebar-menu-grows-past-the-viewport)
- [ ] **Same-page hash navigation leaves the old session selected.**
  [Deslop F16](../agents/audits/2026-11-20-002-deslop.md#f16-same-page-hash-navigation-leaves-the-old-session-selected)
- [ ] **The file viewer alters carriage returns and colors substitutions as
  strings.**
  [Deslop F17](../agents/audits/2026-11-20-002-deslop.md#f17-highlighting-alters-carriage-returns-colors-substitutions-as-strings-and-its-test-misses-recomputation)
- [ ] **The configuration accepts choices with no name.**
  [Deslop F18](../agents/audits/2026-11-20-002-deslop.md#f18-the-catalog-accepts-choices-with-no-name)
- [ ] **`..` is cleaned before symlinks resolve** in `-socket` and the file
  tree's link classification.
  [Deslop F19](../agents/audits/2026-11-20-002-deslop.md#f19--is-cleaned-before-symlinks-resolve)
- [ ] **`make test` fails on a fresh checkout, and the metrics hook fails on
  duplicate files.**
  [Deslop F20](../agents/audits/2026-11-20-002-deslop.md#f20-make-test-fails-on-a-fresh-checkout-and-the-metrics-hook-fails-on-duplicate-content)
- [ ] **`ox-plan` lets an added requirement skip approval, and its dev check
  is not runnable.**
  [Deslop F21](../agents/audits/2026-11-20-002-deslop.md#f21-ox-plan-lets-a-new-requirement-skip-approval-and-its-dev-check-is-not-runnable)
- [ ] **Protocol, architecture, and Claude notes misstate the code.**
  [Deslop F22](../agents/audits/2026-11-20-002-deslop.md#f22-protocol-architecture-and-claude-notes-misstate-the-code)
- [ ] **Small dead code and retained state.**
  [Deslop F23](../agents/audits/2026-11-20-002-deslop.md#f23-small-dead-code-and-retained-state)

## Approvals

- [ ] **Allow always.** Decide whether to persist grants in the workspace or
  in Ox before exposing [Claude's persistent permission option](claude-code.md#can_use_tool).
