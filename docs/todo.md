# Todo

Gaps in features Ox already supports, ordered by priority.

## Check first

- [ ] **Plan mode can change without Ox knowing.** Ox never reads a permission
  mode change from Claude's output. Check live with Haiku: start a session in
  `plan`, approve `ExitPlanMode`, and see whether the next approval request or
  the `system` `init`/status lines show a different mode. Repeat for
  `EnterPlanMode` from `manual`. If approving `ExitPlanMode` leaves plan mode,
  the page shows "Plan" while Claude edits files, which is above the fix
  threshold (permissions other than shown). A resumed process returns to the
  stored mode ([per-process flags](claude-code.md#persistent-session)), so the
  mismatch lasts until the process parks.

## Prompting

- [ ] **Render replies as Markdown.** Replies and the `ExitPlanMode` plan show
  as plain text.

## Approvals

- [ ] **Answer `AskUserQuestion`.** Show the questions and options as a form
  and allow with `updatedInput` holding the original input plus `answers`
  (`docs/claude-code.md` §5). Today Allow echoes the input with no answers.
- [ ] **Allow always.** Offer it when `permission_suggestions` is non-empty
  and `suppress_always_allow_rule` is false, sending the suggestions as
  `updatedPermissions`. The suggestions usually target `localSettings`, which
  writes `.claude/settings.local.json` in the workspace; decide whether that is
  acceptable before building it.

## Sessions and workspaces

- [ ] **Remove a workspace.** Needs a new method and a store delete; neither
  exists. Decide what happens to its sessions (destroy them, or refuse while
  any exist).
