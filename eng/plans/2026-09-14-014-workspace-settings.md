# A workspace settings surface reached from the sidebar

## Goal

Authentication, client-supplied MCP servers, and support details live in one
workspace settings surface that the sidebar navigates to, and the primary column
holds either the selected conversation or those settings. Today the connect form
renders above the conversation whenever authentication is required, and a
`Workspace settings` disclosure holding MCP servers and support details is
stacked below it.

This finishes the sidebar restructuring, so completing it runs the repository's
completeness and simplification review for that top-level work.

`eng/mockups/workspace-settings.html` is the settled structure. It reuses
`client/public/styles.css` unchanged, because settings render inside the
existing scrolling primary column.

## Related code

- `client/src/browser.tsx:32` — `App`, which owns the socket, the MCP draft, the
  credential value, and the `main` tree this change splits into a conversation
  view and a settings view.
- `client/src/browser.tsx:291` — `Sidebar`, which already carries each
  workspace's new, remove, and restart actions and its conversation links.
- `client/src/browser.tsx:357` — `Authentication`, the primary-column connect
  form, and `client/src/browser.tsx:381` — `SupportDetails`, whose nested
  `Authentication` disclosure duplicates it.
- `client/src/host.ts:232` — the `select-workspace` command, implemented and
  currently unused by the browser, which is how a settings link reaches a
  workspace the browser is not showing.
- `client/src/host.ts:269` — `snapshotFor`, which publishes `workspace` and
  `authentication` for the selected workspace only.
- `client/e2e/smoke.spec.ts:1148` — `assertReady`, `openSupportDetails`, and
  `openMCPSettings`, the helpers every settings scenario navigates through.

## Decisions

Workspace settings are a primary-column view, not a sidebar panel and not a
disclosure under the transcript. The sidebar stays navigation: each workspace
entry carries a `Settings` link beside its new and remove buttons, and the link
of the selected workspace carries `aria-current="page"` while settings are
shown. Which view the browser shows is browser state, not host state, so a
refresh returns to the conversation.

The settings link sends `select-workspace`, because the host publishes MCP
counts, diagnostics, and authentication for the selected workspace only. That is
also the only way to reach the settings of a workspace whose Ox cannot
authenticate, since it lists no conversations to open. Everything the view
renders, including its workspace heading and which settings link is current,
derives from `workspaces.selectedId`, so a slow or refused selection shows the
workspace it is actually showing rather than the one that was clicked.

Settings hold three sections with headings rather than nested disclosures. A
disclosure existed to keep this material from competing with the transcript, and
a separate view already does that.

One authentication section serves both connecting and managing: the
stored-credential button while unauthenticated, the advertised terminal login
forms, and logout when available. Its controls are named from the agent's
advertised method names, which is what the support-details variant already did,
so the hardcoded `Connect OpenRouter` heading and `Save credential` button are
removed rather than kept as a second surface.

A workspace that needs a credential shows an ordinary paragraph linking to its
settings in the conversation column. It is not `role="alert"`: the existing
unavailable alert is the one assertive message, and a second live region would
make `getByRole("alert")` ambiguous. The unavailable alert now points at
workspace settings, which is where diagnostics moved.

The MCP draft and its message reset when the selected workspace changes. The
draft is applied to whichever workspace is selected, and following another
workspace's settings link makes carrying it across workspaces reachable.

## Test plan

- `client/e2e/smoke.spec.ts` — the sidebar's `Settings` link opens the settings
  view, which holds authentication, MCP servers, and support details, and a
  conversation link returns to the transcript. Replaces `openSupportDetails` and
  `openMCPSettings`.
- `client/e2e/smoke.spec.ts` — following the `Settings` link of a workspace the
  browser is not showing selects it: its settings name that workspace. Uses the
  two-workspace fixture.
- `client/e2e/smoke.spec.ts` — a workspace whose stored credential cannot
  authenticate shows no authentication form in the conversation column, and
  connecting in its workspace settings opens a conversation. Extends the
  existing stored-credential scenario, which keeps its proof that a failed
  browser login is reported and its secret never reaches the page.
- `client/e2e/smoke.spec.ts` — an incomplete MCP draft survives navigating to a
  conversation and back to settings and does not block that navigation. Replaces
  the MCP scenario's step that edited the draft while the conversation was
  visible.
- `client/e2e/smoke.spec.ts` — the unavailable alert points at workspace
  settings, and the process list and diagnostics are reachable there.

## Implementation plan

- Add the settings view state to `App` in `client/src/browser.tsx`: opening a
  conversation, starting one, and following a settings link set it, and the
  settings link also submits `select-workspace`.
- Add the per-workspace `Settings` link to `Sidebar`, before its conversation
  list so conversation-link queries stay scoped to that list.
- Replace the `Workspace settings` disclosure and the primary-column
  `Authentication` block with a `WorkspaceSettings` section rendered in place of
  the conversation, holding the unified authentication section, the MCP section
  with its configured count and form, and the support section. Remove the nested
  authentication disclosure from `SupportDetails`.
- Render the not-connected pointer in the conversation column and retarget the
  unavailable alert at workspace settings.
- Reset the MCP draft and message when `workspaces.selectedId` changes.
- Update `client/e2e/smoke.spec.ts` and its helpers for the new navigation,
  roles, and names.

## Documentation updates

- `docs/spec.md` — workspace settings also contain authentication, they are
  reached from the navigation region, and they replace the conversation while
  shown.
- `eng/client-architecture.md` — the product surface: settings are a primary
  column view reached from the sidebar and contain authentication, MCP servers,
  and support details; authentication no longer replaces the conversation; a
  workspace is also selected by opening its settings.
- `eng/todo.md` — check off the workspace settings task, which completes the
  sidebar restructuring.
