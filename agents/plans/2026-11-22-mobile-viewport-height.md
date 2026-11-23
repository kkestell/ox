# Size the page to the visible area on phones

## Goal

On iPhone Safari the page is `100dvh` tall, which the on-screen keyboard does
not shrink. When the keyboard opens, Safari pans the page to keep the focused
composer in view, so the session header and folder row leave the top of the
screen ([#15](https://github.com/kkestell/ox/issues/15)), and the composer keeps
its home-indicator margin under a keyboard that already covers the home
indicator, leaving blank space between the two
([#9](https://github.com/kkestell/ox/issues/9)).

When the change is done, the page occupies exactly the visible area: the header
sits at its top and the composer's bottom edge at its bottom, above the home
indicator when the keyboard is closed and directly above the keyboard when it
is open. The transcript takes what is left between them. The drawers and the
card of pending approvals fit the same visible area.

## Related code

- `cmd/ox/web/src/style.css` — `.layout` sets `height: 100dvh`; the composer,
  `nav`, `aside.files .tree`, and `.viewer [role="tabpanel"]` add
  `env(safe-area-inset-bottom)` at the bottom; the narrow layout's drawers and
  `.scrim` are `position: fixed` against the whole window; `.pending` caps
  approvals at `50vh`.
- `cmd/ox/web/src/index.tsx` — the page's entry point, where page-lifetime
  setup runs before `render`.
- `cmd/ox/web/src/App.tsx` — `Layout` renders `.layout`, with the sidebar,
  scrim, `.center`, and file tree as its children.
- `cmd/ox/web/e2e/workspace.spec.ts` — the `on a phone` describe (390×844,
  touch, mobile) covers the narrow layout.
- `cmd/ox/web/e2e/session.spec.ts` — the `on a touch screen` describe, and the
  `ask` helper that raises an approval.
- `cmd/ox/web/e2e/ui.ts` — shared page helpers.

## Decisions

- **The visual viewport is the single source of the page's size.** Browsers
  that keep the keyboard out of the layout (Safari always; Chrome on Android by
  default) report the visible area only through `window.visualViewport`: its
  `height` shrinks when the keyboard opens and its `offsetTop` grows when the
  browser pans. No CSS unit tracks it on Safari, which ignores
  `interactive-widget`. A new `cmd/ox/web/src/viewport.ts` exports
  `followViewport()`, which `index.tsx` calls before `render`. It writes three
  custom properties on `document.documentElement` and rewrites them on the
  visual viewport's `resize` and `scroll` events:
  - `--viewport-top`: `visualViewport.offsetTop` in px.
  - `--viewport-height`: `visualViewport.height` in px.
  - `--keyboard-height`: `max(0, innerHeight - visualViewport.height)` in px,
    the part of the window the keyboard covers. `innerHeight` follows Safari's
    toolbars but not the keyboard.

  The listeners live for the page; there is nothing to clean up. No app state
  is involved, so no store or signal holds these values. Every supported
  browser has `visualViewport`; there is no fallback path.
- **The layout is fixed to the visible area.** `.layout` becomes
  `position: fixed; left: 0; right: 0; top: var(--viewport-top); height: var(--viewport-height)`,
  replacing `height: 100dvh`. It follows the browser's pan instead of fighting
  it, so a pan that arrives before the resize event settles once the event
  does. The document no longer has in-flow content taller than the window, so
  nothing scrolls the page itself.
- **One bottom inset, derived in CSS.** `.layout` defines
  `--bottom-inset: max(0px, env(safe-area-inset-bottom) - var(--keyboard-height))`.
  The four places that add `env(safe-area-inset-bottom)` (the composer's bottom
  margin, `nav`'s bottom padding, `aside.files .tree`'s bottom padding,
  `.viewer [role="tabpanel"]`'s bottom padding) use `var(--bottom-inset)`
  instead. With the keyboard closed it equals the home-indicator inset; with
  the keyboard open it is 0, so the composer sits its usual margin above the
  keyboard. This resolves the keyboard-open half of #9.
- **Drawers and scrim belong to the layout.** `.narrow nav`,
  `.narrow aside.files`, and `.scrim` change from `position: fixed` to
  `position: absolute`. `.layout`, now fixed, is their containing block, so a
  drawer opened while the keyboard is up spans the visible area instead of
  running under the keyboard. The sidebar's `.menu` is a popover in the top
  layer and is unaffected.
- **Pending approvals cap at half the visible area.** `.pending`'s
  `max-height: 50vh` becomes `calc(var(--viewport-height) / 2)`, so typing an
  answer into a question's text field leaves the composer above the keyboard.
- **Pinch zoom leaves the layout alone.** While `visualViewport.scale > 1`,
  `followViewport` ignores events and the layout keeps its last size and
  position, so zooming magnifies the page as it does on any other site. Without
  this, a pinch zoom on a phone or a desktop trackpad would shrink the layout
  into the zoomed area. Returning to scale 1 fires `resize`, which brings the
  layout back to the visible area.
- **The wide layout uses the same path.** On a desktop window the visual
  viewport equals the window, so the result matches `100dvh` there; there is no
  narrow-only branch.
- **Requirements:** **Touch screens** ("the page keeps its controls clear of
  the notch, rounded corners, and browser toolbars") and **Narrow screens**
  hold, and now also hold while the keyboard is open. The text stays as it is:
  it already describes the intended behavior, and the fix shows no requirement
  was wrong. No request to `oxd` is involved.
- **Construction:** the page's height and top have one source, the visual
  viewport, read in one place, `viewport.ts`. The bottom inset has one
  definition, `--bottom-inset`, which every bottom edge reads.
- **What only a real iPhone confirms.** Headless Chromium has no on-screen
  keyboard and does not pan, so the specs drive the visual viewport directly
  (see the test plan). They prove the page follows whatever the visual viewport
  reports. Only an iPhone in Safari confirms that:
  - Safari reports the keyboard through `visualViewport.height` and its pan
    through `offsetTop` in the iOS version in use, including after the keyboard
    closes (iOS 26 had reports of `offsetTop` not returning to 0).
  - `innerHeight` stays put when the keyboard opens, so `--keyboard-height`
    grows past the home-indicator inset.
  - The keyboard-closed band in #9 is gone. Whether that band came from the
    composer's inset or from `100dvh` disagreeing with the visible area under
    Safari's toolbar cannot be reproduced in Chromium; sizing to the visual
    viewport addresses the second cause, and the first is the intended
    home-indicator margin.
  - The frame between Safari's pan and the `resize` event does not visibly
    jump.

## Check your assumptions

None.

## Test plan

Chromium emulates the phone's insets with the CDP command
`Emulation.setSafeAreaInsetsOverride` (present in the installed Chrome 157),
and the keyboard by giving `window.visualViewport` its own `height`,
`offsetTop`, and `scale` getters and dispatching `resize` on it, as Safari does
when the keyboard opens and pans the page. Add to `cmd/ox/web/e2e/ui.ts`:

- `setSafeArea(page, { top, bottom })`: opens a CDP session on the page and
  sends `Emulation.setSafeAreaInsetsOverride`.
- `openKeyboard(page, { height, offsetTop, scale = 1 })`: in the page, defines
  own getters on `visualViewport` for `height` (`innerHeight - height`),
  `offsetTop`, and `scale`, then dispatches `resize`.
- `closeKeyboard(page)`: deletes those own properties and dispatches `resize`.

All three tests run at 390×844 with insets top 47, bottom 34; the narrow
composer margin is 8 px.

- **The header and composer stay at the visible area's edges as the keyboard
  opens and closes** — e2e, new test in `workspace.spec.ts`'s `on a phone`
  describe, in a session. Keyboard closed: the session header's top is 47 and
  the composer's bottom is 802 (844 − 34 − 8). `openKeyboard` with height 336
  and `offsetTop` 120: the header's top is 167 and the composer's bottom is 620
  (120 + 508 − 8, no home-indicator inset). `closeKeyboard`: back to 47 and
  802. `openKeyboard` with `scale` 2: both stay at 47 and 802. Fails before the
  change, where the header stays at 47 and the composer at 802 with the
  keyboard open. No existing test measures the layout's edges.
- **A drawer spans the visible area while the keyboard is open** — e2e, new
  test in the same describe. With the keyboard open as above, Toggle sessions
  opens the session list, whose box runs from 120 to 628. Fails before the
  change, where the drawer runs from 0 to 844. The existing drawer tests check
  opening and closing, not geometry.
- **Pending approvals leave the composer above the keyboard** — e2e, new test
  in `session.spec.ts`'s `on a touch screen` describe. With the keyboard open
  as above, raise an approval with `ask` whose expanded input is about 40 lines
  long; the `Waiting on you` region is at most 254 px tall (half of 508) and
  the composer's bottom is 620. Fails before the change, where the region grows
  to 422 px and pushes the composer below the keyboard.

## Implementation plan

1. Create `cmd/ox/web/src/viewport.ts` with `followViewport()` as described,
   with a comment stating why the page follows the visual viewport (the
   keyboard does not resize the layout viewport on Safari).
2. Call `followViewport()` in `cmd/ox/web/src/index.tsx` before `render`.
3. In `cmd/ox/web/src/style.css`:
   - `.layout`: replace `height: 100dvh` with the fixed positioning above and
     define `--bottom-inset`. Update the comment above `.layout` to say the
     layout fills the visible area and what sits at the bottom takes
     `--bottom-inset`.
   - Replace `env(safe-area-inset-bottom)` with `var(--bottom-inset)` in the
     composer, `nav`, `aside.files .tree`, and `.viewer [role="tabpanel"]`.
   - `.narrow nav, .narrow aside.files` and `.scrim`: `position: absolute`.
   - `.pending`: `max-height: calc(var(--viewport-height) / 2)`.
4. Add the helpers to `cmd/ox/web/e2e/ui.ts` and the three tests above.
5. Check in headless Chrome through `make dev` at 390×844 with the same
   insets and keyboard emulation that the session view, a drawer, and the file
   viewer look right with the keyboard open and closed, and that the wide
   layout is unchanged.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, the web UI's file table: a new row for
  `viewport.ts`, which sizes the page to the browser's visible area for the
  life of the page.
