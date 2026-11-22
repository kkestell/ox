# Mobile layout

## Goal

On a phone the page keeps its desktop layout. In iPhone Safari at about 390 pt
wide, the session list and file tree take their 288 px each, and the transcript
is squeezed to nothing between them
(`/Users/kyle/Desktop/2026-11-22 07.51.21.jpg`). Session rows hide their New
session and actions buttons until hover, which a touch screen never gives, and
the composer's 14 px text makes Safari zoom the page on focus.

When the change is done, a narrow screen shows one pane at a time, as the
ChatGPT and Claude mobile web apps do: the transcript fills the width, the
session list is a drawer that slides in from the left over a dimmed page and
closes when the user picks a session, and the file tree is the same drawer on
the right. Opening a file shows it in place of the transcript until its tab is
closed. Touch screens show the hover-only buttons, give rows a touch-sized
height, and keep text fields at 16 px. The page stays clear of the notch, the
rounded corners, and Safari's floating toolbar.

## Related code

- `cmd/ox/web/src/Panels.tsx` — `createPanels` holds which side panels show
  and their widths; `PanelToggles` and `Sash`.
- `cmd/ox/web/src/App.tsx` — `Layout` places the sidebar, sashes, `main`, the
  file viewer, and the file tree; decides where the toggles sit (`tree`,
  `Toggles`); `select` changes the selection.
- `cmd/ox/web/src/Sidebar.tsx` — `nav`, kept mounted while hidden so its Show
  archived choice lasts; its width comes from `Layout`.
- `cmd/ox/web/src/FileTree.tsx` — `aside.files`, unmounted while hidden;
  `FileRow` opens a file through the control.
- `cmd/ox/web/src/FileView.tsx` — `.viewer`, the tab and its close button.
- `cmd/ox/web/src/style.css` — `#root` is the page's flex row at `height: 100%`;
  the sidebar's row geometry (`nav li` at 2rem, the child guides' `--rise`
  arithmetic); hover-revealed row buttons (`opacity: 0`); `.composer` margin
  and `--transcript-inset: 37px`; `.panel-toggles`; `.sash`; `.viewer`.
- `cmd/ox/web/index.html` — the viewport meta tag.
- `cmd/ox/web/e2e/workspace.spec.ts` — "panel toggles and drags change the
  layout, which lasts until reload" covers the wide layout at 1600 px.

## Decisions

- **Breakpoint.** A viewport narrower than 768 px is narrow. The query string
  lives once, in `Panels.tsx`; `Layout` puts the result on its root element as
  the `narrow` class, and every narrow style in `style.css` is under `.narrow`.
  Touch styles use `@media (pointer: coarse)`, a separate fact about the
  device: a touch screen above 768 px gets taller rows and visible buttons in
  the wide layout.
- **Drawer state.** `createPanels` adds `narrow` (a signal following
  `matchMedia("(max-width: 767px)")`) and `drawer`, of type
  `"sessions" | "files" | undefined`, so at most one drawer can be open. The
  wide layout keeps its `left` and `right` booleans and widths, untouched by
  the narrow layout, so a narrow excursion of a desktop window returns to the
  panels it had. Callers read only the derived `sessions()` and `files()`
  (`narrow() ? drawer() === … : left()`/`right()`) and call
  `toggleSessions()`, `toggleFiles()`, and `dismiss()`; `setLeft` and
  `setRight` go. Crossing the breakpoint clears `drawer`, so a drawer never
  reappears from an earlier narrow spell.
- **Drawer at load.** `createPanels` takes the drawer to start with. `Layout`
  passes `"sessions"` when the URL names no session, since `main` is empty
  then, and `undefined` otherwise. It is read only while narrow.
- **Closing drawers.** A tap on the scrim, picking a session or a new-session
  draft in the sidebar, and opening a file in the tree close the drawer. The
  sidebar's `onSelect` in `Layout` calls `select` then `panels.dismiss()`;
  `select` itself does not, so a creation finishing (`finish`) or a hash
  change leaves a drawer the user opened alone. `FileTree` takes an `onOpen`
  prop that `FileRow` calls after `setFile`; `Layout` passes `panels.dismiss`.
  `dismiss` is a no-op in the wide layout, since it clears only `drawer`.
- **Drawer rendering.** The sidebar stays mounted and slides: under `.narrow`,
  `nav` is fixed to the left edge at `width: min(18rem, 85vw)` with a shadow,
  and `nav[hidden]` keeps `display: flex` with `visibility: hidden` and
  `translate: -100% 0`, transitioning `translate` and `visibility` over 0.2 s.
  `visibility: hidden` keeps its buttons out of the tab order. The file tree
  keeps unmounting while hidden, so its polling stops; under `.narrow` it is
  fixed to the right edge at the same width and slides in with
  `@starting-style { translate: 100% 0 }`. `Layout` passes `width` to `Sidebar`
  and `FileTree` only while wide; both make the prop optional and omit the
  inline width without it. A `div.scrim` (fixed, full screen, below the
  drawers, `rgb(0 0 0 / 50%)` like `dialog::backdrop`) renders while
  `narrow() && (sessions() || tree() !== undefined)`, and its `onClick` is
  `panels.dismiss`. Pressing the files toggle with nothing selected opens no
  drawer and no scrim, as the wide layout shows no tree then.
- **Root element.** `Layout` returns
  `<div class={["layout", { narrow: panels.narrow() }]}>` around its children.
  `#root`'s rules move to `.layout`, with `height: 100dvh` so the composer sits
  above Safari's toolbar, and
  `padding: env(safe-area-inset-top) env(safe-area-inset-right) 0 env(safe-area-inset-left)`.
  The viewport meta tag gains `viewport-fit=cover`. The bottom inset goes on
  what sits at the bottom, so content scrolls beneath the toolbar: `.composer`'s
  bottom margin, the drawers' and the file tree's and viewer's bottom padding.
  The fixed drawers also take the top and their outer side's inset.
- **Toggles.** Narrow, the toggles always sit in `main`'s header, never in the
  file tree's: `Layout` derives
  `togglesInTree = () => !panels.narrow() && tree() !== undefined`, shows
  `Toggles` when it is false, and passes `toggles` to `FileTree` only when it
  is true (`FileTree.toggles` becomes optional). Under `.narrow`,
  `.panel-toggles` is `display: contents`, the sessions toggle takes
  `order: -1` before the title, and the files toggle stays at the right, as the
  menu button sits left of the title in the ChatGPT and Claude mobile headers.
  `aria-pressed` reads `sessions()` and `files()`, so a toggle is in full color
  while its drawer is open.
- **One pane.** Under `.narrow`, `main:has(~ .viewer)` is `display: none`, so an
  open file replaces the transcript and the tab's close button returns to it;
  `.viewer` loses its left border; `.sash` is `display: none`, so no panel
  resizes. No JS state backs either: the open file is already the control's
  `file`.
- **Touch.** Under `@media (pointer: coarse)`: the sidebar's row buttons
  (`nav > p button.icon`, `nav li button.icon`) are always at full opacity;
  session rows and file tree rows are 2.5rem tall; `textarea`, text `input`s,
  and `select` are 16 px, the size below which Safari zooms on focus; the
  composer's model picker and permission-mode select take `font-size: 1rem` and
  `height: 2rem` so they match. The sidebar's guide arithmetic reads a `--row`
  variable (2rem by default) in place of its literal `2rem` row and `1rem` dot
  offset (`calc(var(--row) / 2)`), so taller rows keep their guides aligned.
- **Composer margin.** A `--composer-margin` variable (1rem, 0.5rem under
  `.narrow`) sets `.composer`'s margin, and `--transcript-inset` becomes
  `calc(var(--composer-margin) + 21px)`, keeping the transcript in line with
  the composer's icons at both sizes.
- **Requirements.** Panel toggles, Resizing panels, Layout through navigation,
  and File viewer gain their wide-screen scope (Requirement changes); Narrow
  screens and Touch screens are added. Unsent text, Open file, and File tree
  expansion hold unchanged: the drawers and the one-pane rule read the
  existing controls, and the sidebar stays mounted, so Showing archived
  sessions keeps its choice through the drawer closing. Session URL holds:
  reloading at a session's URL opens it with the drawer closed. No request is
  added, so the request lifecycle concerns do not apply.
- **Construction.** At most one drawer is open: `drawer` is a single value.
  The narrow layout cannot read the wide panel booleans, nor the wide the
  drawer: both read through `sessions()` and `files()`. The breakpoint has one
  source, the query in `Panels.tsx`, which CSS reads through `.narrow`.
- **Ownership.** `narrow`, `drawer`, and the `matchMedia` change listener
  belong to `createPanels`, inside `Layout`, for the life of the page. The
  behaviors that need them: one pane at a time below 768 px, and a drawer that
  opens and closes. Race: a breakpoint change while a drawer is open; the
  listener clears `drawer`, so the wide layout shows its own panels and a later
  narrow spell starts closed. Cleanup: an `onSettled` cleanup removes the
  listener when `Layout` is disposed. Nothing is stored; a reload starts from
  the URL.

## Check your assumptions

1. **Added requirement: Narrow screens.**
   - **Plan:** Below 768 px wide, the page shows one pane at a time: the
     transcript, or an open file in its place until its tab is closed. The
     session list slides in from the left and the file tree from the right,
     over a dimmed page, from the buttons at the two ends of the session's
     header; a tap outside, picking a session or new-session draft, or opening
     a file closes them. Panels cannot be resized. 768 px is where the ChatGPT
     and Claude web apps switch, so an iPhone in portrait gets this layout and
     an iPhone in landscape (844–932 px) or an iPad keeps the desktop one,
     where both side panels leave the transcript 268–356 px on a landscape
     phone. Text: "**Narrow screens.** On a screen narrower than 768 px, the
     page shows one pane at a time: the transcript, or the open file in its
     place until its tab is closed. The session list and the file tree slide
     in over it from the left and right, from the buttons at either end of the
     session's header, and close when the user taps outside them, picks a
     session or a new-session draft, or opens a file; panels cannot be
     resized."
   - **Alternative:** Switch at 1024 px, so landscape phones and portrait
     iPads also get one pane at a time; a narrow laptop window would too.
2. **The session list opens by itself at load when no session is named.**
   - **Plan:** Opening Ox on a phone at its bare URL shows the session list
     drawer, since the pane behind it is empty; opening a session's URL shows
     the session with the drawer closed. Text appended to Narrow screens: "At
     load, the session list is open when the URL names no session, and closed
     otherwise."
   - **Alternative:** Always start closed, as ChatGPT does; the bare URL then
     shows an empty pane with only the two buttons until the user taps one.
3. **Added requirement: Touch screens.**
   - **Plan:** On any touch screen, each session row shows its actions button
     and each workspace row its New session and actions buttons all the time,
     rows are taller, and tapping a text field does not zoom the page. Text:
     "**Touch screens.** On a touch screen, session and file rows are tall
     enough to tap, the session list's New session and actions buttons always
     show, focusing a text field does not zoom the page, and the page keeps
     its controls clear of the notch, rounded corners, and browser toolbars."
   - **Alternative:** Keep the buttons hidden until a long press. iPhone Safari
     sends no long-press event the page can use for this, so New session and
     the menus would be unreachable on an iPhone.
4. **Changed requirements: the desktop layout rules apply to wide screens.**
   - **Plan:** The four layout and file requirements that describe side-by-side
     panels name their scope, so they do not contradict Narrow screens:
     - Panel toggles. Old: "They sit at the right of the file tree's header
       while the tree shows, and otherwise at the right of the session's
       header, whether or not a file is open." New: "On a screen 768 px or
       wider, they sit at the right of the file tree's header while the tree
       shows, and otherwise at the right of the session's header, whether or
       not a file is open."
     - Resizing panels. Old: "Dragging the boundary beside …" New: "On a
       screen 768 px or wider, dragging the boundary beside …"
     - Layout through navigation. Old: "… reloading the page shows both panels
       at their default widths, …" New: "… reloading the page on a screen
       768 px or wider shows both panels at their default widths, …"
     - File viewer. Old: "… opens it in a pane between the transcript and the
       tree, under a tab …" New: "… opens it in a pane between the transcript
       and the tree, or in place of the transcript on a narrow screen, under a
       tab …"
   - **Alternative:** Leave them as written and let Narrow screens override
     them; the requirements would then disagree on a phone.

## Test plan

- New e2e test in `cmd/ox/web/e2e/workspace.spec.ts`, "on a phone the page
  shows one pane at a time", in a `test.describe` with
  `test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })`.
  No existing test runs at a narrow viewport or with touch. It first asserts
  `matchMedia("(pointer: coarse)").matches`; if Chromium's touch emulation does
  not report a coarse pointer, it sets one through CDP
  `Emulation.setEmulatedMedia` with `features: [{ name: "pointer", value: "coarse" }, { name: "hover", value: "none" }]`.
  Steps, using `tap()`:
  - At the bare URL, `nav` is visible, `.scrim` is visible, and `main` is
    390 px wide beneath them. `open` adds the workspace through the dialog.
  - The workspace row's New session button has computed opacity 1. Tapping it
    hides `nav` and `.scrim`; the prompt's computed font size is 16px.
  - Send a prompt and wait for the reply. The Toggle sessions button's box is
    left of the `h1`'s, Toggle files is right of it, and no separator is
    visible.
  - Tap Toggle files: the Files region is visible, contains no toggle, and
    Toggle files is pressed. Tap the scrim at x 20 (outside the right drawer):
    the region is gone. Reopen it, expand `src`, tap `a.txt`: the region is
    gone, the File region is visible at 390 px wide, and `main` is hidden. Tap
    Close a.txt: `main` is visible again.
  - Tap Toggle sessions: `nav` is visible, Toggle sessions is pressed, and the
    session row is 40 px tall. Tap the session row: `nav` is hidden. Reload:
    the session shows and `nav` is hidden.
  - `setViewportSize({ width: 1000, height: 844 })`: `nav` and the Files region
    show side by side with `main`, `main`'s width is 1000 minus theirs, and the
    Resize sessions separator is visible. Open the sessions drawer at 390 px
    first, then widen and narrow again: `nav` is hidden on return.
- Bottom inset: in the same test, if the installed Chrome supports CDP
  `Emulation.setSafeAreaInsetsOverride`, set a 34 px bottom inset and assert
  the composer's bottom edge is at least 34 px above the viewport's bottom.
  The implementer probes for the method first; without it, the work log names
  the inset as covered only by the user's check on the phone.
- The wide layout keeps its coverage: "panel toggles and drags change the
  layout, which lasts until reload" runs at 1600 px unchanged; it guards that `sessions()`/`files()` and
  the moved toggles leave the wide layout unchanged. "a child's guide starts
  at its parent's dot or where its previous sibling branches" in
  `session.spec.ts` guards the `--row` rewrite of the guide arithmetic at its
  2rem default.

## Implementation plan

1. `cmd/ox/web/index.html`: viewport content
   `width=device-width, initial-scale=1, viewport-fit=cover`.
2. `cmd/ox/web/src/Panels.tsx`: `createPanels(drawerAtLoad)` with `narrow`,
   `drawer`, the `matchMedia` listener registered in `onSettled` that sets
   `narrow` and clears `drawer`, the derived `sessions()` and `files()`,
   `toggleSessions()`, `toggleFiles()`, `dismiss()`, and the existing widths;
   drop `left`, `right`, `setLeft`, `setRight` from the returned object.
   `PanelToggles` reads `sessions()`/`files()` and calls the toggles. Update
   the comment on `createPanels`.
3. `cmd/ox/web/src/Sidebar.tsx`: `width?: number`; omit the inline width
   without it.
4. `cmd/ox/web/src/FileTree.tsx`: `width?: number`, `toggles?: JSX.Element`,
   and `onOpen(): void`, carried on `Tree` and called by `FileRow` after
   `setFile`.
5. `cmd/ox/web/src/App.tsx`: wrap the output in the `.layout` div; create
   panels with `selectionFromHash() ? undefined : "sessions"`; `tree` reads
   `panels.files()`; add `togglesInTree`; render the scrim; the sidebar's
   `onSelect` selects then dismisses; pass widths only while wide; render
   the sessions sash on `panels.sessions()`; pass `onOpen={panels.dismiss}`
   and the conditional `toggles` to `FileTree`.
6. `cmd/ox/web/src/style.css`: move `#root`'s rules to `.layout` with
   `100dvh` and the safe-area padding; `--composer-margin` and the derived
   `--transcript-inset`; `--row` in the sidebar's row height and guide
   arithmetic; the `.narrow` block (drawers, `nav[hidden]` slide, file tree
   `@starting-style`, scrim, toggles order, `main:has(~ .viewer)`, viewer
   border, sash, composer margin); bottom insets on the composer, drawers,
   tree, and viewer; the `@media (pointer: coarse)` block. Leave the
   uncommitted `.files > header` edit from the Tailscale work in place.
7. `cmd/ox/web/e2e/workspace.spec.ts`: the new test.
8. `docs/functional-requirements.md`: the approved text below.
9. `docs/architecture.md`: the row for the components says `Layout` owns
   which panels show and their widths in the wide layout, and which drawer is
   open in the narrow one, below 768 px, for the life of the page.

## Requirement changes

- **Added** under Layout, after Layout through navigation:
  - **Narrow screens.** On a screen narrower than 768 px, the page shows one
    pane at a time: the transcript, or the open file in its place until its
    tab is closed. The session list and the file tree slide in over it from
    the left and right, from the buttons at either end of the session's
    header, and close when the user taps outside them, picks a session or a
    new-session draft, or opens a file; panels cannot be resized. At load, the
    session list is open when the URL names no session, and closed otherwise.
  - **Touch screens.** On a touch screen, session and file rows are tall
    enough to tap, the session list's New session and actions buttons always
    show, focusing a text field does not zoom the page, and the page keeps its
    controls clear of the notch, rounded corners, and browser toolbars.
- **Changed:** Panel toggles. Old: "Two buttons show and hide the session list
  and the file tree, each in full color while its panel shows and dimmed while
  it is hidden. They sit at the right of the file tree's header while the tree
  shows, and otherwise at the right of the session's header, whether or not a
  file is open." New: "Two buttons show and hide the session list and the file
  tree, each in full color while its panel shows and dimmed while it is
  hidden. On a screen 768 px or wider, they sit at the right of the file
  tree's header while the tree shows, and otherwise at the right of the
  session's header, whether or not a file is open."
- **Changed:** Resizing panels. Old: "Dragging the boundary beside the session
  list, the file tree, or between the transcript and the file viewer resizes
  them." New: "On a screen 768 px or wider, dragging the boundary beside the
  session list, the file tree, or between the transcript and the file viewer
  resizes them."
- **Changed:** Layout through navigation. Old last sentence: "The layout lasts
  through switching views and reconnecting; reloading the page shows both
  panels at their default widths, with the transcript and file viewer
  splitting evenly." New: "The layout lasts through switching views and
  reconnecting; reloading the page on a screen 768 px or wider shows both
  panels at their default widths, with the transcript and file viewer
  splitting evenly."
- **Changed:** File viewer. Old first sentence: "Clicking a file in the file
  tree opens it in a pane between the transcript and the tree, under a tab
  with its name and a close button, and highlights its row; clicking another
  file replaces it." New: "Clicking a file in the file tree opens it in a pane
  between the transcript and the tree, or in place of the transcript on a
  narrow screen, under a tab with its name and a close button, and highlights
  its row; clicking another file replaces it."

## Documentation updates

- `docs/architecture.md`: the web UI components row, as in task 9.
