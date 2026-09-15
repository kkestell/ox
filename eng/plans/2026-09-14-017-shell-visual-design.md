# Shell visual design

## Goal

The browser client has a real stylesheet but no shape: one undifferentiated
two-column layout, and nothing usable on a phone. Give the shell its appearance:
a sidebar of grouped workspaces and conversations, workspace registration behind
a dialog, a conversation header carrying the title and workspace, a footer
pinned to the bottom of the column holding the composer with the model, mode,
reasoning and context row, workspace settings on their own surfaces, and a
navigation drawer below desktop width.

The transcript's own presentation — tool activity, plans, permissions,
questions, and auto-scroll that yields to manual scrollback — is the
conversation styling task's work and only receives placement here.

`eng/mockups/shell.html` is the settled appearance. Its `layout`, `sidebar`,
`conversation` and `settings` layers are this task's addition to
`client/src/styles.css`, and its five frames are the states to match: desktop
conversation, desktop workspace settings, the add-workspace dialog, phone with
the navigation closed, and phone with it open.

## Related code

- `client/src/components/app.tsx` — Owns the root element, the primary column,
  the alert paragraphs, the conversation header and the workspace settings
  section.
- `client/src/components/sidebar.tsx` — The whole navigation region, including
  the registration form that becomes a dialog. Its path field and submit are
  driven from `app.tsx` state.
- `client/src/components/session-information.tsx` — Moves from the conversation
  header to the footer beneath the composer.
- `client/src/components/authentication.tsx`,
  `client/src/components/mcp-activation-form.tsx`,
  `client/src/components/support-details.tsx` — The three settings sections,
  each labelled by its own `h3`, which one `.settings > section` rule styles.
- `client/e2e/smoke.spec.ts:48` — Asserts the sidebar's right edge is at or left
  of `main`'s left edge. The suite runs at Playwright's default 1280x720
  viewport, above the drawer breakpoint, so the existing scenarios exercise the
  wide shell.

## Decisions

The shell asks its own width with a container query rather than a media query,
and `container-type: inline-size` sits on the mount root. The breakpoint is
written once. Nothing reads the viewport in JavaScript, so there is no hook, no
`matchMedia` subscription, and no second rendered copy of the navigation: one
`<nav>` is inline beside the primary column when there is room and a fixed
overlay when there is not.

Because CSS decides what the drawer state means, React only owns the boolean. It
sets `data-navigation="open"` on the root, and at wide width that attribute
styles nothing. React additionally handles the Escape key and marks the primary
column `inert` while the drawer is open — the two things an overlay owes a
keyboard user that CSS cannot supply.

Opening a conversation, creating one, and opening a workspace's settings clear
that attribute, because the drawer covers the surface the command just changed.

The drawer's only trigger is a narrow-width top bar owned by the shell. With no
workspace registered, no credential connected, or no conversation selected, the
primary column shows a message and nothing else, so a trigger belonging to the
conversation header would strand a phone user in exactly the state where the
navigation matters most. That bar carries the `h1`; the sidebar carries it
instead when it is inline, so exactly one exists at any width.

The primary column is three bands that do not scroll as one — header, scrolling
middle, footer — so the composer and the session's controls sit on the bottom
edge at any window height and the settings surface scrolls in the same middle
band.

The model, mode, reasoning and context row sits beneath the composer rather than
in the conversation header. Those controls describe what the next message will
use, so they belong beside the control that sends it, and the header is then one
row of identity rather than two rows competing for the top of the surface.

Registering a workspace moves out of the sidebar's footer into a `<dialog>`
opened by an add control in the sidebar's own header. A permanent labelled field
and submit button carried the visual weight of the most-used control in the
region while being the least-used one. `showModal()` supplies the backdrop,
Escape and focus containment. The trigger is named `Add workspace` and the
submit keeps the name `Register workspace`, because both are present while the
dialog is open. A rejected path keeps the dialog open with its message; a
successful registration closes it and clears the field. The empty registry's
prompt stays in the sidebar body, where it is the reason to reach for the
control.

Sidebar controls that become icon-only — new conversation, settings, remove —
keep the `aria-label` they already have and gain a `title` with the same
meaning.

A conversation waiting for an answer is marked with a dot and the unchanged
`Waiting for you` text on its meta line, the same treatment as `Opening…`, not a
badge. The sidebar already carries a selected row, timestamps and per-workspace
controls, and a badge outweighs all of them.

Every status and measurement string keeps its current wording: `Waiting for
you`,
`Opening…`, `Ox is starting.`, `Ox is not running.`, the `Context:` usage
sentence, and the registration prompt.

## Test plan

- The registration scenario opens the dialog, fills the path and submits. An
  unlistable path leaves the dialog open showing its message; a valid one closes
  it and adds the workspace. One helper describes that flow for all three
  registrations in the scenario.
- A narrow-viewport scenario at 390x844: no navigation region until the trigger
  is pressed; the drawer then exposes the same workspace and conversation
  controls; choosing a conversation closes it and leaves that conversation
  showing; Escape closes it; the primary column is not reachable by keyboard
  while it is open.
- The existing wide-viewport scenarios must pass unchanged. The ones this change
  can break are the sidebar-beside-`main` geometry, the single-match
  `getByRole("alert")` assertions, `Waiting for you` inside a conversation list,
  `Context:` inside the session information region, the `Settings for <name>`,
  `Remove <name>` and `Restart <name>` names that become icon-only, and the
  `Workspace path` label that now exists only while the dialog is open.

## Implementation plan

- Add the mockup's `layout`, `sidebar`, `conversation` and `settings` layers to
  `client/src/styles.css`, and `container-type` to the mount root.
- In `app.tsx`: give the primary column its three bands, add the narrow-width
  top bar with the drawer trigger and the `h1`, own the drawer boolean with its
  Escape handler and `inert`, clear it from the new, open and settings
  callbacks, and mark up the alert paragraphs as banners.
- Reduce the conversation header to one row of title, workspace name and the
  actions disclosure, and move `SessionInformation` into the pinned footer with
  the composer.
- Mark up `sidebar.tsx` to the mockup: a header with the wordmark and the add
  control over a scrolling list, workspace groups with icon actions,
  conversation links with their selected state, timestamp and waiting and
  opening indicators, and the not-running state with its restart control.
- Add `client/src/components/add-workspace-dialog.tsx` and move the registration
  form into it, keeping the failure message inside and closing on success.
- Mark up the workspace settings surface as the three labelled sections.
- Add the narrow-viewport scenario and the registration helper to
  `client/e2e/smoke.spec.ts`.

## Documentation updates

- `eng/client-architecture.md` — The product surface section says the navigation
  region sits beside the primary column, holds workspace registration, and that
  the conversation header contains the model, mode and reasoning controls with
  context usage. Record that it sits beside the primary column when there is
  room and opens as an overlay drawer when there is not, with one rendered
  navigation region either way; that registration opens as a dialog from it; and
  that the session's controls and context usage sit with the composer.
- `eng/todo.md` — Check off the shell styling task.
