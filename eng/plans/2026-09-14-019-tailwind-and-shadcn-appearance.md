# Tailwind and shadcn appearance

## Goal

The browser client's appearance is Tailwind utilities over shadcn's neutral base
theme, with every control that shadcn ships coming from a vendored shadcn
component. The layout keeps its shape — sidebar beside the primary column,
drawer at phone width, conversation as header, transcript, interaction tray, and
composer — while the visual details follow shadcn's defaults rather than a
hand-tuned stylesheet.

## Related code

- `client/src/styles.css` — The Tailwind entry and base theme the CLI compiles
  into `client/public/styles.css`, which becomes generated output.
- `client/src/components/ui/` — Vendored shadcn components. They are project
  source: `Card` and `CardTitle` gain `asChild` so a card can render the
  `section` or `article` that carries the region's accessible name.
- `client/src/components/sidebar.tsx` — Renders shadcn's sidebar block; the
  drawer, overlay, and focus trap come from it rather than from local CSS.
- `client/src/components/composer.tsx` — Owns the prompt row and the Escape
  handler that must yield to whichever overlay is on top.
- `client/e2e/smoke.spec.ts` — Proves the ACP-visible product workflows through
  accessible roles and names.

## Decisions

Tailwind compiles through `@tailwindcss/cli` ahead of `bun build`, and
`client/public/styles.css` is generated and ignored. The base theme is shadcn's
neutral palette selected by `prefers-color-scheme`, so there is no theme toggle
and no `.dark` class.

shadcn's sidebar block owns the navigation region at both widths. Its desktop
column and its mobile `Sheet` replace the local drawer, scrim, `inert`
management, and Escape handling. Workspaces are a `SidebarMenu` and each
workspace's conversations are its `SidebarMenuSub`, which is what keeps
`Workspaces` and `<name> conversations` addressable as named lists.

Radix controls change three roles. The configuration and single-choice
elicitation controls become comboboxes, conversation actions become a dropdown
menu, and disclosures become collapsibles. A disclosure still renders its
content only while open, so a long transcript does not pay for collapsed tool
output. A multiple-selection field keeps the platform's own `select`, because
Radix has no multiple-selection listbox.

Cards carry no accessible name of their own. Where a card is a labelled region
or a pending interaction, it renders through `asChild` onto the `section` or
`article` that already owned that name.

The composer's textarea grows through `field-sizing: content` against a maximum
height instead of a measured layout effect. Escape reaches a running turn only
when no dialog, sheet, menu, or select content is mounted.

## Test plan

- Keep every existing `client/e2e/smoke.spec.ts` scenario, updated where a
  control's role changed: menu items for conversation actions, comboboxes for
  mode and elicitation choices, and the drawer's modal semantics in place of
  `inert`.
- Hover assertions read the settled background colour, because control fills
  animate.

## Implementation plan

- Add the Tailwind and shadcn toolchain, `components.json`, the `@/*` path
  mapping, and the base theme; put the Tailwind CLI in front of the bundle and
  the typecheck.
- Vendor the shadcn components the client uses and give `Card` and `CardTitle`
  `asChild`.
- Rewrite each component module onto those primitives and Tailwind utilities,
  replacing the hand-written SVG icons with `lucide-react`.
- Delete `client/public/styles.css` from the repository and ignore it.

## Documentation updates

- `eng/client-architecture.md` — Appearance and control ownership.
- `eng/todo.md` — The styling subtree's first item.
