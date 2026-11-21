# Todo

Planned work, ordered by priority: unfixed audit findings, then gaps in features
Ox already supports. Each finding links its section in the audit, which holds
the evidence and the correction.

## Defects

- [ ] **Reply text lost on an abrupt exit.** Text shown live is not stored until
      the next non-text update, so a killed `oxd` loses it. Bound the unstored
      text, or reword **Surviving restarts**; the user decides.
      [Deslop F1](../agents/audits/2026-11-20-002-deslop.md#f1-reply-text-shown-live-is-lost-on-an-abrupt-oxd-exit)
- [ ] **One Ctrl-C forces `oxd` past graceful shutdown under `make dev`.** The
      terminal's `SIGINT` and the stack's `SIGTERM` both reach `oxd`.
      [Deslop F2](../agents/audits/2026-11-20-002-deslop.md#f2-one-ctrl-c-under-make-dev-forces-oxd-past-graceful-shutdown)
- [ ] **Archive and unarchive act on a stale session.** Unarchive can flip a
      recreated session's stored flag, and archive replies before its members
      have stopped.
      [Deslop F4](../agents/audits/2026-11-20-002-deslop.md#f4-archive-and-unarchive-act-outside-the-owner-of-session-membership)
- [ ] **A second `oxd` can share the database.** Only the socket is exclusive,
      so a daemon on another socket repairs and archives the first one's
      sessions.
      [Deslop F6](../agents/audits/2026-11-20-002-deslop.md#f6-only-the-socket-not-the-database-has-one-owner)
- [ ] **A race report from a stopping `oxd` is lost.** e2e and dev restarts
      ignore the exit status of the daemon they stop.
      [Deslop F7](../agents/audits/2026-11-20-002-deslop.md#f7-a-race-report-from-a-stopping-oxd-is-lost-in-e2e-and-dev-restarts)
- [ ] **Tests that pass when their behavior breaks.** Six tests, from the
      settings passed to the next start to `TestLive`'s ordering.
      [Deslop F8](../agents/audits/2026-11-20-002-deslop.md#f8-tests-that-pass-when-the-behavior-they-name-breaks)
- [ ] **`ox-plan` lets an added requirement skip approval, and its dev check is
      not runnable.**
      [Deslop F21](../agents/audits/2026-11-20-002-deslop.md#f21-ox-plan-lets-a-new-requirement-skip-approval-and-its-dev-check-is-not-runnable)
- [ ] **Protocol, architecture, and Claude notes misstate the code.**
      [Deslop F22](../agents/audits/2026-11-20-002-deslop.md#f22-protocol-architecture-and-claude-notes-misstate-the-code)

- [ ] **Workspace headers shrink when archived sessions are shown.** The extra
      rows overflow the sidebar, and its flex column shrinks each header below
      its `1.75rem` height. Likely fix: `flex: none` on `nav > p` in
      `cmd/ox/web/src/style.css`.

## Sidebar

- [ ] **Collapse a session's children.** A session with children gets a
      disclosure control that hides and shows them. Decide whether the collapsed
      state survives a reload.
- [ ] **Newest sessions first.** List each workspace's top-level sessions newest
      first. Decide whether children follow, or stay oldest first so a plan,
      work, and review read in order. Changes **Session order**.

## Child sessions

- [ ] **Consistent child titles.** Agents title children inconsistently
      ("Implement" and "Work" for the same stage, "Plan F1: …" beside "Plan:
      …"), and the sidebar truncates the topic that tells siblings apart. Give
      `create_session`'s description in `internal/server/mcp.go` a title
      convention.

## Questions

- [ ] **One question per page.** A form with several questions shows them all in
      one scrolling container. Show one question at a time with Previous and
      Next buttons, and submit from the last. Changes **Questions from Claude**.

## Approvals

- [ ] **Allow always.** Decide whether to persist grants in the workspace or in
      Ox before exposing
      [Claude's persistent permission option](harnesses/claude-code.md#can_use_tool).
