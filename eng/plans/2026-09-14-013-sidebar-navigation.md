# One sidebar navigation region

## Goal

Workspace registration, every workspace's conversation list, and the
conversation the browser is showing live in one sidebar navigation region, the
selected conversation is the only primary content, and a minimal layout
stylesheet places the sidebar beside it. Today `WorkspacePicker`, `History`, the
conversation, and workspace settings are stacked siblings in one unstyled
column, and a conversation waiting in a workspace the browser is not showing is
reachable only through a separate "Workspaces waiting for an answer" list.

`eng/mockups/sidebar-layout.html` is the settled structure and stylesheet.

## Related code

- `client/src/browser.tsx:189` — `App`'s returned tree, the stacked column this
  change splits into a navigation region and one primary column.
- `client/src/browser.tsx:297` — `WorkspacePicker`, which owns the workspace
  `select`, removal, the waiting list, and the registration form.
- `client/src/browser.tsx:369` — `History`, already a `nav`, which reads the
  selected workspace entry's `conversations`.
- `client/src/host.ts:35` — `mimeTypes`, which the asset handler consults and
  which has no `.css` entry.
- `client/e2e/smoke.spec.ts:1107` — `assertReady` and the neighboring helpers,
  which name the surfaces every scenario navigates through.

## Decisions

Each registered workspace is a sidebar entry carrying its own conversation list,
because the host already publishes bounded recent conversations for every entry
and retains any conversation that is waiting. A waiting conversation is then
reachable where it belongs, so the separate "Workspaces waiting for an answer"
list is removed rather than restyled, and the workspace-level `awaiting` flag
keeps only its support-details use.

Navigating is a link and acting is a button. Conversations are links, so the
sidebar reads as navigation to assistive technology and to the eye; the current
one carries `aria-current="page"`. The link's `href` is a fragment naming the
conversation. It is an anchor for the link semantic, not a restore path: the
host stays authoritative for selection and the application ignores the fragment
on load.

A workspace name is a heading rather than a control. There is no
workspace-selection control at all, because opening or creating a conversation
already selects its workspace. Each workspace heading carries a `+` button
labeled `New conversation in <workspace>` and a `Remove <workspace>` button, so
a workspace whose Ox died and therefore lists no conversations can still be
removed. A workspace that is not ready shows its problem and a
`Restart <workspace>` button in place of its conversation list, which is what
replaces selecting a broken workspace to reach its recovery. `select-workspace`
stays in the browser protocol and the host; the sidebar no longer sends it.

The conversation header carries the selected workspace's name beside the
conversation title, so the primary content says which workspace it belongs to
without depending on a sidebar highlight that does not exist yet.

Paging stays on the selected workspace's entry, matching the host contract where
only the selected workspace carries a complete list and `nextCursor`.

The stylesheet is a static `client/public/styles.css` served beside
`client/public/index.html`, not a bundled import, so the layout does not depend
on the browser bundle. It sets only the flex row, the sidebar width, and
independent scrolling; color, type, and spacing are not part of this change.

Authentication, MCP servers, and support details stay in the primary column.
Moving them into a workspace settings surface is separate work.

## Test plan

- `client/e2e/smoke.spec.ts` — port every scenario that switched workspaces
  through the `Current workspace` select, read conversation history, or asserted
  the displayed workspace from `main > header` onto the sidebar's per-workspace
  lists, its conversation links, and the conversation header.
- `client/e2e/smoke.spec.ts` — a conversation waiting in the workspace the
  browser is not showing is listed under that workspace in the sidebar, and
  following its link selects that workspace and shows the pending permission.
  This replaces the waiting-list step in the permission-routing scenario.
- `client/e2e/smoke.spec.ts` — a workspace whose Ox stopped offers its restart
  in the sidebar, and removing a workspace no longer depends on selecting it.
- `client/e2e/smoke.spec.ts` — the sidebar renders beside the primary column:
  its bounding box ends at or before the primary column's left edge. This is
  also what proves the stylesheet is served with a usable content type.

## Implementation plan

- Add `client/public/styles.css` with the layout rules from the mockup, link it
  from `client/public/index.html`, and add `.css` to `mimeTypes` in
  `client/src/host.ts`.
- Replace `WorkspacePicker` and `History` in `client/src/browser.tsx` with one
  `Sidebar` component: the `Ox` heading, a workspace list whose entries carry
  the workspace heading, its new and remove actions, its restart when it is not
  ready, its conversation links, and the selected entry's older page, then the
  registration form and its message.
- Restructure `App`'s tree into the layout wrapper, the sidebar, and a `main`
  holding the workspace alert, authentication, session error, the conversation,
  and workspace settings. Add the workspace name to the conversation header and
  drop the application header that carried it.
- Update `client/e2e/smoke.spec.ts` and its helpers for the new roles and names.

## Documentation updates

- `eng/client-architecture.md` — replace the pre-CSS invariant with the durable
  rule that the browser surface stays semantic HTML with native controls, that
  navigation is links and action is buttons, and that styling carries layout
  rather than behavior or accessible naming; drop the trailing gate sentence
  from the testing boundary; record the sidebar navigation region in the product
  surface.
- `docs/spec.md` — conversation navigation is one region beside the selected
  conversation, which is the only primary content.
- `eng/todo.md` — check off the sidebar navigation task.
