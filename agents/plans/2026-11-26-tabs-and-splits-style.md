# Rewrite the stylesheet on design tokens

## Goal

`cmd/ox/web/src/style.css` grew by small patches. Colors, lengths, font sizes,
radii, shadows, durations, and z-indexes are written as literals across 666
lines; one element's rules sit in two or three places; ghost buttons undo the
default button look a dozen times; and some selectors match nothing. Rewrite
the file from scratch on a token system in `:root`, in a fixed section order,
with every component rule written in tokens. A Node test in `make test` keeps
it that way. The page looks the same in light and dark, wide and phone, except
for the short list of intentional changes under Decisions.

This plan sits on the `tabs-and-splits` branch, after the commit that adds the
tab and split layout. Its slug carries `-style` because that branch already has
a plan of its own.

## Related code

- `cmd/ox/web/src/style.css`: the whole stylesheet, rewritten.
- `cmd/ox/web/src/*.tsx`: the class names the rules match. Only the button and
  text-field markup changes (task 3).
- `cmd/ox/web/src/Panels.tsx` (`span`, line 91): reads the panels' computed
  `min-width` and `max-width` for the sash's bounds, so CSS stays the single
  source of those limits.
- Inline custom properties the stylesheet reads but does not define: `--depth`
  and `--rise` (`Sidebar.tsx`, `FileTree.tsx`), `--tree` (`App.tsx`),
  `--digits` (`FileView.tsx`, `Changes.tsx`), `--p` (`SettingsFields.tsx`), and
  Shiki's `--shiki-light-font-style` and `--shiki-light-font-weight`.
- `cmd/ox/web/e2e/hero.spec.ts`: plays `docs/screenshots/hero.json` through the
  scene player. The parity capture (task 1) starts from its setup.
- `Makefile` (`test`): runs the Node suites. It gains `style.test.mjs`.
- `cmd/ox/web/biome.json`: Biome's CSS formatter is off and its CSS linter is
  on. The rewritten file passes `npm run lint`.

## Decisions

- **Requirements:** No requirement changes. The rewrite must keep **Panel
  toggles**, **Tabs**, **Splitting tab groups**, **Resizing panels**, **Narrow
  screens**, **Touch screens**, and **Line numbers** as they are. The parity
  capture and `make e2e` check this. No request lifecycle is involved.

- **Token system.** `:root` holds every design value, in these groups and
  under these names. Values come from the current file. Where a token names
  a role, components use it for that role only.
  - *Palette:* the current shadcn/ui neutral palette and app colors, values
    unchanged: `--background`, `--foreground`, `--text`, `--card`, `--popover`,
    `--muted`, `--muted-foreground`, `--primary`, `--primary-foreground`,
    `--secondary`, `--secondary-foreground`, `--accent`, `--destructive`,
    `--added`, `--modified`, `--border`, `--input`, `--input-background`,
    `--ring`, `--sidebar`, `--sidebar-accent`, `--sidebar-selected`, `--pane`,
    `--composer`. The colors now written in rules join it:
    `--destructive-foreground` (white), `--primary-hover` and
    `--destructive-hover` (90% mixes), `--secondary-hover` (80%), `--overlay`
    (`rgb(0 0 0 / 50%)`, dialog backdrop and scrim), `--focus-ring` (ring at
    50%), `--drop-target` (ring at 25%), `--scrollbar-thumb`
    (muted-foreground at 50%), `--line-added` and `--line-removed` (15% mixes
    into `--pane`), `--usage-track` (muted-foreground at 20%), `--warning`
    (amber, `oklch(0.769 0.188 70.08)`), `--critical`
    (`oklch(0.637 0.237 25.331)`), `--status-idle` (`#525252`),
    `--status-running` (`#16a34a`), `--status-running-bright` (`#4ade80`), and
    `--slider-thumb` (white). The radio's checked dot, a gradient, is the image
    token `--radio-checked`. The checkbox's check mask stays a `url()` in its
    rule, like the icon masks.
  - *Type:* `--font-sans`, `--font-mono`; sizes `--text-2xs` 0.6875rem (11px),
    `--text-xs` 0.75rem, `--text-sm` 0.8125rem, `--text-md` 0.875rem (the
    body), `--text-lg` 0.9375rem, `--text-xl` 1rem, `--text-2xl` 1.0625rem,
    `--text-3xl` 1.125rem; Markdown heading sizes `--text-h1`
    `calc(1em + 0.25rem)` and `--text-h2` `calc(1em + 0.125rem)`; weights
    `--font-normal` 400, `--font-medium` 500, `--font-semibold` 600; line
    heights `--leading` 1.5, `--leading-sm` 1.25rem, `--leading-lg` 1.5rem,
    `--one-line` 1lh; `--digit-width` 1ch.
  - *Spacing:* `--space-px` 1px, `--space-0-5` 0.125rem, `--space-1` 0.25rem,
    `--space-1-5` 0.375rem, `--space-2` 0.5rem, `--space-2-5` 0.625rem,
    `--space-3` 0.75rem, `--space-4` 1rem, `--space-5` 1.25rem, `--space-6`
    1.5rem, `--space-8` 2rem; and `--inset` (`var(--space-4)`), the composer's
    edge that the header rows, transcript, and alerts align to. `--inset`
    replaces `.layout`'s `--composer-margin` and `--inset`. Negative margins
    are `calc(-1 * var(--space-…))`.
  - *Radii:* `--radius` 0.625rem; `--radius-xs` 0.25rem, `--radius-sm`
    `calc(var(--radius) - 4px)`, `--radius-md` `calc(var(--radius) - 2px)`,
    `--radius-lg` `var(--radius)`, `--radius-xl` `calc(var(--radius) + 4px)`,
    `--radius-2xl` 1rem, `--radius-full` 9999px. Every `50%` circle and
    `9999px` pill takes `--radius-full`.
  - *Borders and rings:* `--border-width` 1px, `--border-width-md` 1.5px,
    `--border-width-lg` 2px, `--ring-width` 3px, `--ring-width-lg` 4px, and
    `--focus-outline` (`var(--ring-width) solid var(--focus-ring)`), used by
    the global `:focus-visible` rule and by the composer's select.
  - *Shadows:* `--shadow-xs`, `--shadow-sm`, `--shadow-md`, `--shadow-lg`
    unchanged, and `--drop-shadow-lg`, the composer box's `filter` while
    approvals are pending.
  - *Sizes:* `--icon-size` 1rem, `--icon-size-sm` 0.75rem; `--header-height`
    2.75rem (renamed from `--context-title`); `--row-height` 2rem,
    `--row-height-sm` 1.75rem, `--touch-height` 2.5rem; `--control-height`
    2rem, `--control-height-sm` 1.5rem, `--control-height-lg` 2.25rem,
    `--touch-control-width` 2.25rem; `--icon-button-size` 1.75rem,
    `--icon-button-size-sm` 1.25rem; `--status-dot` 0.5rem; `--marker-width`
    1.5rem (also the tool card's left margin, which aligns under the name);
    `--session-indent` 1.25rem; `--tree-indent` 1rem; `--panel-min-width`
    10rem; `--panel-max-width` 40rem; `--group-min-width` 15rem;
    `--group-min-height` 8rem; `--drawer-width` `min(18rem, 85vw)`;
    `--dialog-width` 32rem; `--menu-min-width` 11rem; `--menu-max-width` 18rem;
    `--popover-width` 20rem; `--popover-max-width` `calc(100vw - 2rem)`;
    `--tab-max-width` 16rem; `--prompt-max-width` 75%; `--image-max-height`
    16rem; `--list-max-height` 16rem; `--scroll-max-height` 20rem;
    `--pending-max-height` 50cqh; `--composer-max-height`
    `calc(8lh + 0.5rem)`; `--thumbnail-size` 4rem; `--usage-bar-width` 4rem;
    `--usage-bar-width-narrow` 3rem; `--usage-bar-height` 0.5rem;
    `--slider-track-height` 0.375rem; `--slider-thumb-size` 1rem;
    `--tick-length` 0.375rem; `--ticks-height` 2.25rem.
  - *Layers:* `--z-raised` 1 (sashes, the row chevron over its title),
    `--z-overlay` 2 (drop preview, scrim), `--z-drawer` 3.
  - *Motion:* `--duration-fast` 0.1s, `--duration` 0.2s, `--delay-hover` 0.3s,
    `--duration-loop` 1s (spinner and running dot).
  - *Opacity:* `--opacity-faint` 0.5 (disabled controls, the search icon,
    the usage reset time), `--opacity-dim` 0.6 (queued prompts, archived and
    parked rows, the effort name).

  Panel limits and the group minimum move from px to the equal rem, and every
  px font size becomes rem; this is the same at the default 16px root size.
  Each replaced literal is rewritten as a token expression with the same
  computed value. For example, the select's right padding is
  `calc(var(--space-2) + var(--icon-size) + var(--space-1-5))`, the slider
  thumb's offset is
  `calc((var(--slider-track-height) - var(--slider-thumb-size)) / 2)`, and the
  sidebar row's focus-ring room is `--ring-width`. Comments that restate a
  value, such as "indents 1.25rem", name the token instead.

- **The literal rule.** `style.test.mjs` enforces it exactly. Rules whose
  selector is `:root`, at the top level or inside an at-rule, are exempt. In
  every other declaration, after removing `url(…)`, quoted strings, and
  custom property names:
  - No hex color, no color function (`rgb`, `rgba`, `hsl`, `hsla`, `hwb`,
    `lab`, `lch`, `oklab`, `oklch`, `color`, `color-mix`, `light-dark`), and
    no `white` or `black`. `transparent` and `currentColor` are keywords.
  - No number, except:
    - zero, with or without a unit;
    - `50%`, `100%`, and their negatives;
    - an angle in `deg`;
    - `1fr`;
    - unitless `1` in `flex`, `flex-grow`, `flex-shrink`, and `opacity`;
    - a unitless integer that multiplies or divides inside `calc()`
      (`-1 * …`, `… * 2`, `… / 2`).

  Keywords, `var()`, `calc()`, `min()`, `max()`, `clamp()`, `env()`,
  `counter()`, `attr()`, and gradients over tokens are free. So z-index,
  font weight, line height, and opacity other than 0 and 1 come only from
  tokens. The same test also fails on `!important` anywhere, on two rules with
  the same selector text in the same at-rule context, on a `var()` naming a
  custom property that neither the stylesheet nor the inline list in Related
  code defines, and on a `:root` token that nothing reads.

- **File structure.** One file, in this order, each section headed by a
  one-line comment naming its component file:
  1. Reset.
  2. Tokens: `:root`, grouped as above.
  3. Base elements: `body`, scrollbars, the focus ring, the `.icon` mask
     and the icon map, buttons and their variants, text fields, radio and
     checkbox, `.toggle-group`, alert and status text, and `dialog`.
  4. Layout (`App.tsx`, `Panels.tsx`): `.layout`, `.center`, `.panes`, the
     center's alerts, sashes, panel toggles, and the narrow layout's drawers
     and scrim.
  5. One section per component, in this order: `Sidebar.tsx` (nav, rows,
     tree guides, status dots, the context menu); `TabArea.tsx` (splits,
     cells, groups, strips, tabs, editor actions, drop preview);
     `TabPicker.tsx`; `SessionView.tsx` (log, transcript, prompts, tool and
     reasoning rows, the composer and its box, pending approvals, the jump
     button; `NewSessionView.tsx` reuses these classes and has no section);
     `Markdown.tsx`; `Attachments.tsx`; `ApprovalRequest.tsx`;
     `SettingsFields.tsx` (model picker, select, effort slider);
     `Usage.tsx`; `HarnessIcon.tsx`; `Workspace.tsx` (the side panel and its
     header); `FileTree.tsx`; `Changes.tsx` (the Changes list, counts, branch
     summary, change cards, hunks); `FileView.tsx` (the viewer, line numbers,
     preview, Shiki styles).
  6. Touch screens: the one `@media (pointer: coarse)` block, its rules in the
     section order above.

  Each element's rules live in its component's section, in one rule per
  selector. Shared declarations use a selector list or `:is()` instead of a
  copy, such as one truncation rule for `:is(.tab, .tab-picker, .tab-menu)
  .label` and one `--at` rule for `:is(.effort input[type="range"], .ticks
  span)`. Comments that explain a non-obvious reason carry over.

- **Buttons.** The base `button` rule becomes a reset: inline flex, centered,
  no border, background, or shadow, inherited color and weight, the
  `--radius-md` corner, and a pointer cursor. Shadcn's variants become
  classes: `.outline` (the current default look), `.primary`, and
  `.destructive`, sharing one height and padding rule; `button.icon` is the
  ghost icon button. `button[type="submit"]` no longer styles itself. This
  removes every rule that undoes the default look and every
  `:hover { background: transparent }`. Text fields follow the same idea: the
  field style matches `input[type="text"]` only, so the model picker's search
  input needs no reset.

- **Selectors removed:** `nav > [role="alert"]`, which matches nothing (the
  sidebar's alert is inside `.workspaces`); `nav .workspaces > section`, which
  has no effect in a block container; `nav > .bar > button[aria-pressed="false"]`
  and `.files > header > button.panel-right[aria-pressed="false"]`, copies of
  the panel toggle rule; `.strip.bare .editor-actions`, a copy of
  `.strip .editor-actions`; `.harness-icon.icon`, whose `display: block` has
  no effect because both places that render it make it a flex item; and
  `.prompt .markdown pre code`, replaced by a `--code-background` property
  that `.markdown` sets and `.prompt .markdown` overrides. The paired rules
  `.viewer pre.wrap code` and `.hunk pre.wrap code`, the two `.error` rules,
  the two `.composer .settings > button.picker` rules, and the two
  `nav :is(header, li) .actions` rules each become one.

- **Intentional visible changes:**
  - The effort slider's tick labels go from 10px to 11px, joining
    `--text-2xs`.
  - The waiting status dot takes `--warning`, the usage bar's amber, in place
    of the near-identical `#f59e0b`.
  - Text, panel limits, and slider parts set in px now follow the browser's
    default font size, as the rest of the page already does. At the default
    size nothing moves.

- **Parity.** The parity capture compares the page before and after, pixel by
  pixel, in light and dark, through a temporary Playwright spec built on the
  scene player. `cmd/ox/web/AGENTS.md` forbids screenshot comparisons in
  specs, so the spec and its baseline images stay uncommitted. They live in
  the worktree through review, so the reviewer can rerun the comparison, and
  the top-level agent deletes them before committing.

- **Construction:** The test is the single check of the token rule. `:root` is
  the single source of each value, and `Panels.tsx` keeps reading the panel
  limits from computed style.

## Check your assumptions

None.

## Test plan

- The token rule, `!important`, duplicate selectors, and undefined or unused
  custom properties: a new `cmd/ox/web/style.test.mjs` under `node:test`, with
  one test per check. It reads `src/style.css` and parses it with a small
  parser in the test: strip comments; walk braces outside strings and
  parentheses; record each rule's selector, its at-rule context, and its
  declarations. It adds no dependency. No existing suite reads the
  stylesheet. On the current `style.css`, the literal test fails on hundreds
  of literals and the duplicate test fails on `nav :is(header, li) .actions`.
  The `!important` and custom property tests pass on it, so each is shown to
  fail against a scratch copy with an `!important`, an undefined `var()`, or
  an unused token. Each failure message names the selector, property, and
  offending text.
- Visual parity: the temporary `cmd/ox/web/e2e/parity.spec.ts` (task 1).
- Behavior through the CSS (drawers, sashes, the narrow strip, the tab menu
  staying in view, toggles' `aria-pressed`): the existing `make e2e` specs,
  unchanged.

## Implementation plan

1. **Baseline, before any edit.** Copy `e2e/hero.spec.ts` to
   `e2e/parity.spec.ts`, skipped unless `OX_PARITY=1`. Keep its scene setup.
   Before the page loads, give the harbor-themes workspace a modified, an
   added, and a deleted file. Replace its captures with
   `expect(page).toHaveScreenshot(name, { maxDiffPixels: 0 })` for each view
   below, once with `colorScheme: "light"` and once with `"dark"`. Wide views
   use 1600×1000 at device scale 2; phone views use `devices["iPhone 15"]`,
   whose touch emulation applies `pointer: coarse`.
   - wide session "Back off sync retries" with the Files panel open, a file
     tab split to the right group (drag it as `workspace.spec.ts`'s
     `dropOn` does), and one tool call expanded;
   - the same view with a session row hovered (its actions and chevron show)
     and with keyboard focus on a strip button (the focus ring);
   - the Changes panel and the Changes tab for "Document theme tokens";
   - `notes/2026-11-18-planning.md` in preview, and in source with wrap on,
     in the archived session "Summarize planning notes", shown through Show
     archived;
   - "Raise muted text contrast" with its approval card;
   - the session actions menu with a disabled item, the workspace actions
     menu, the Add workspace dialog, and the Delete session dialog;
   - the model picker popover with the effort slider;
   - a new-session draft;
   - phone: the session with its approval card, the tab picker's menu open,
     the session list drawer, and the Files drawer.

   Run it twice on the untouched tree
   (`TMPDIR=/tmp OX_SCREENSHOTS=1 OX_PARITY=1 npx playwright test parity
   --update-snapshots`, then without the flag) to confirm every shot is
   stable. Mask any region that is not, such as a relative time. Then start
   the rewrite.
2. **`style.test.mjs` and the Makefile.** Write the test as the Test plan
   describes, and add it to the `node --test` list in `Makefile`'s `test`
   target. Confirm it fails on the current stylesheet.
3. **Markup.** Add `class="outline"` to the Reload and Reopen draft buttons
   (`App.tsx`), the question card's Decline and Back buttons
   (`ApprovalRequest.tsx`), the Unarchive button (`SessionView.tsx`), and the
   dialog's Cancel button (`Sidebar.tsx`). Give the dialog's submit button
   `class={request.kind === "delete" ? "destructive" : "primary"}` and its
   Directory input `type="text"` (`Sidebar.tsx`). Check with `grep` that no
   other button relies on the old default look.
4. **Rewrite `src/style.css`** in the structure and tokens above. Write it
   fresh, section by section, against the markup, not by editing the old
   file in place. Run `node --test style.test.mjs` and `npm run lint` until
   both pass.
5. **Compare.** Run the parity spec without `--update-snapshots`. Every
   difference must be one of the intentional visible changes. The tick
   labels show in the model picker shots, and the waiting dot shows in the
   sidebar. Fix any other difference in the stylesheet and rerun. Then run
   `make test` and `make e2e`.
6. **Hand off** with the parity spec and its `parity.spec.ts-snapshots/`
   directory still in place, uncommitted, for the review.

## Requirement changes

None.

## Documentation updates

- `cmd/ox/web/AGENTS.md`: the parent session lands this text. Add it after
  the bullet on how the browser places the page:

  ```markdown
  - `src/style.css` takes every design value from the tokens in its `:root`:
    colors, lengths, font sizes and weights, line heights, radii, border and
    ring widths, shadows, opacity, durations, and z-indexes. Other rules use
    those tokens and keywords only, plus the few geometric literals
    `style.test.mjs` allows. A new value joins the scale in `:root`, and a
    value close to an existing token takes that token. The file keeps its
    order (reset, tokens, base elements, layout, then one section per
    component in `src/`, then the touch-screen block), and each element's
    rules live in its component's section.
  ```

  In the bullet on `make test`, the Node suites become
  `highlight.test.mjs`, `stack.test.mjs`, `toolInput.test.mjs`,
  `id.test.mjs`, `diff.test.mjs`, `tabs.test.mjs`, and `style.test.mjs`.
