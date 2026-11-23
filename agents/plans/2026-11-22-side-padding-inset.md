# One side inset for the session view

## Goal

The session view's parts start at different distances from the pane's edges
([#16](https://github.com/kkestell/ox/issues/16)): the header's title and folder
rows at 12px, the composer box at 16px (8px on a narrow screen), the transcript
at 37px (29px), and the model and mode controls at 35px (27px). After the
change, the header rows, the transcript, the composer box, the settings row,
the page's alerts, and the archived composer all line up on one left edge and
one right edge, 1rem from the pane's sides on wide and narrow screens, as
Claude.ai and ChatGPT line their messages up with the composer box's outer
edge. The composer's text keeps its padding inside the box.

## Related code

- `cmd/ox/web/src/style.css` `.layout` — defines `--composer-margin: 1rem` and
  `--transcript-inset`, which puts the transcript on the composer's text.
  `.narrow` sets `--composer-margin: 0.5rem`.
- `.composer` — margin `var(--composer-margin)`, so the box and the archived
  composer's outer edges sit at the composer margin.
- `.context > *` — the session header's title and folder rows, padded `0.75rem`.
- `.panel-toggles`, `.narrow .panel-toggles .panel-left/.panel-right` — the
  icon buttons at the ends of the title row (the wide layout's toggles at its
  right; the narrow layout's at both ends).
- `.files > header` — the file tree's header, whose right padding matches the
  session header's so the toggles keep their place on screen as they move
  between the two (`workspace.spec.ts`, "the toggles show and hide the panels
  from the header they sit in").
- `.transcript`, `.log ~ [role="alert"], .log ~ [role="status"]` — use
  `--transcript-inset`.
- `main > [role="alert"]` — the page-level alerts above the header
  (`App.tsx`), padded `1rem`.
- `.composer .settings` — `margin-top: 0.5rem; padding-left: 11px`; each picker
  and select pads its content by `0.5rem` (`SettingsFields.tsx`; the model
  picker starts with the harness icon).
- `SessionView.tsx`, `NewSessionView.tsx`, `App.tsx` `BareHeader` — share the
  `.context` header and `.composer` markup, so the CSS covers all three views.

## Decisions

- **The shared edge is the composer box's outer edge.** Decided by the
  top-level agent under the user's rule to copy established UI patterns from
  well-known apps exactly (Claude.ai and ChatGPT line messages up with the
  composer box); the user can overturn it in the pull request. Inside the box,
  the composer's text and send button keep their padding.
- **One variable.** `--transcript-inset` becomes `--inset`, defined on `.layout`
  as `var(--composer-margin)`, with its comment naming every part that lines up
  on it. Every side inset in the session view reads `--inset`, so a change to
  `--composer-margin` (as #9 may make) moves every part together.
- **1rem on every screen.** The `.narrow` override of `--composer-margin` goes,
  so the edge is 1rem on a phone too; an 8px edge would put the transcript's
  text against the screen's rounded corners. This also widens the composer's
  bottom margin on a phone from 8px to 1rem above the safe area; #9 owns the
  bottom margin and may change it.
- **Icons align by their glyph.** An icon button at a row's end places its 1rem
  glyph on the edge and lets its hover background hang past it. The 1.75rem
  toggle buttons take a `-0.375rem` margin on their outer side.
- **Settings align by content.** The settings row takes `margin-inline:
  -0.5rem` and no padding, so the model picker's harness icon starts on the
  edge and the pickers' hover backgrounds hang 0.5rem past it, into the
  composer's margin.
- **File tree header.** Its right padding becomes `var(--inset)`, so the wide
  layout's toggles stay in the same place on screen as they move between the
  tree's header and the session's.
- **Out of scope:** the approvals card (`.pending`), which is positioned
  relative to the composer box, the file viewer, and the sidebar.
- **Requirements:** **Panel toggles** and **Narrow screens** hold: the toggles
  stay at the same ends of the same headers and in the same place as they move.
  No request lifecycle is involved.

## Check your assumptions

1. **Everything lines up on the composer box's outer edge.** Changed by the
   top-level agent from lining up on the transcript's current edge.
   - **Plan:** The header title, folder row, transcript, composer box, and model
     and mode controls all start 1rem from the pane's edge, on a wide screen and
     a phone, as in Claude.ai and ChatGPT. The transcript gains about 21px a
     side on a wide screen and 13px on a phone; the composer box loses 8px a
     side on a phone; the composer's text sits inside the box, further in than
     the transcript's.
   - **Alternative:** Line everything up on the transcript's current edge, where
     the composer's text starts (37px wide, 29px phone). The transcript would
     keep its width and the header title would sit further in.

## Test plan

- New e2e test in `session.spec.ts`, run at the default (wide) viewport and in
  a phone `describe` (390×844, touch): after a reply, the left edges of the
  title (`heading` level 1), the folder row's content box, the reply, the
  composer's form (`.box`), and the model picker's harness icon are equal
  within 1px and 1rem from the pane's left; the right edges of the reply and
  the form are equal within 1px. On the phone, the toggles' glyphs (the 1rem
  square centered in each button) also land on the left and right edges. No
  existing test measures these parts, and the title and reply comparisons fail
  on the current CSS.
- Existing `workspace.spec.ts` "the toggles show and hide the panels from the
  header they sit in" keeps the wide toggles' position equal across the two
  headers.

## Implementation plan

1. In `cmd/ox/web/src/style.css`, replace `--transcript-inset` in `.layout`
   with `--inset: var(--composer-margin)` and point its two uses at `--inset`.
   Rewrite the `.layout` comment to name the parts that line up on the edge and
   that icon glyphs at row ends sit on it.
2. Delete `.narrow { --composer-margin: 0.5rem; }`.
3. Set `.context > *` padding to `0 var(--inset)` and `main > [role="alert"]`
   padding to `0.5rem var(--inset)`.
4. Add `margin-right: -0.375rem` to `.panel-toggles`; set `.narrow
   .panel-toggles .panel-left` margin to `0 0.5rem 0 -0.375rem` and add
   `margin-right: -0.375rem` to `.narrow .panel-toggles .panel-right`.
5. Set `.files > header` padding to `0 var(--inset) 0 1rem`.
6. Replace `.composer .settings`'s `margin-top: 0.5rem; padding-left: 11px`
   with `margin: 0.5rem -0.5rem 0`.
7. Add the e2e test above to `cmd/ox/web/e2e/session.spec.ts`.
8. Take before and after screenshots of the session view, the new-session view,
   and the archived composer, wide and on a phone, for the pull request.

## Requirement changes

None.
