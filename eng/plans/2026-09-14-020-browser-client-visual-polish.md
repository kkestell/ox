# Browser client visual polish

## Goal

Finish the browser client's responsive appearance by repairing the overflow,
occlusion, cramped navigation, weak hierarchy, and unfinished empty, composer,
interaction, transcript, and settings surfaces found during phone and desktop
visual verification. Every existing workflow remains available, while long
titles, tool identifiers, paths, diagnostics, and narrow viewports stay within
their owning surface.

## Related code

- `docs/spec.md#web-client` — Owns the navigation, conversation, composer,
  pending-interaction, settings, and support-detail product surfaces that the
  repair must preserve.
- `eng/client-architecture.md#product-surface` — Keeps the repair inside the
  existing sidebar/drawer and primary-column structure.
- `client/src/components/app.tsx` — Owns the shared top band, empty state,
  transcript scrollport, pending-interaction placement, composer placement, and
  settings page shell.
- `client/src/components/sidebar.tsx` and `client/src/components/ui/sidebar.tsx`
  — Own navigation density, workspace actions, selected state, metadata, and
  phone drawer sizing.
- `client/src/components/{transcript,disclosure,pending-interactions,composer,session-information}.tsx`
  — Own transcript wrapping and hierarchy, the jump action, blocking requests,
  and prompt controls.
- `client/src/components/{authentication,mcp-activation-form,support-details,add-workspace-dialog}.tsx`
  — Own settings density, action labels, nested forms, diagnostics, and
  workspace registration guidance.
- `client/src/styles.css` and `client/public/index.html` — Own global dark-theme
  scrollbar treatment and browser identity assets.
- `client/e2e/smoke.spec.ts` — Exercises the real responsive browser surface and
  provides the stable geometry boundary for regressions.

## Decisions

Use the activity title as the collapsed tool label; keep protocol tool names,
kinds, locations, and raw output as clearly labelled disclosed details. Wrap
unbroken technical strings instead of allowing them to widen the transcript, and
reserve layout space for the return-to-latest action so it never covers content.

Keep blocking interactions between the transcript and composer, but present them
as a compact bounded tray. Give the composer one coherent bordered surface with
visible Send and Stop actions, visible labels for configuration selectors, and
context usage that wraps as a complete metadata row on narrow screens.

Keep workspace actions in the navigation region while consolidating secondary
settings and removal actions into one workspace menu. Give new conversation a
labelled row, widen the desktop navigation, and make the phone drawer occupy the
phone viewport so the obscured page cannot remain as a distracting sliver.

Settings receive the same top band as conversations, an explicit Workspace
settings title and return action, denser cards, an authenticated state that does
not resemble an empty credential form, compact nested MCP groups, and wrapped
support diagnostics. No new package or Markdown-rendering behavior is needed.

## Test plan

- Extend the Playwright fixture with long workspace, conversation, tool,
  location, output, and diagnostic strings; at 390px and 800px assert that the
  primary column and transcript have no horizontal overflow.
- Assert that the return-to-latest control stays within a reserved action row,
  the pending tray ends before the composer, and neither covers the transcript.
- At phone width, assert that the open navigation occupies the viewport and
  hides the primary region; at desktop width, assert that navigation and every
  primary page share the same top-band height.
- Assert the labelled workspace menu, new-conversation action, configuration
  controls, Send/Stop actions, centred empty state, settings title and return
  action, concise tool title, disclosed technical labels, and wrapped support
  details through accessible roles and names.
- Run the client checks and real-process Playwright suite, then run the
  top-level completeness and simplification review and rerun affected gates.

## Implementation plan

- Refine the shared shell in `app.tsx`: align conversation and settings headers,
  centre actionable empty states, reserve transcript jump space, and rebalance
  transcript, interaction, and composer sizing.
- Rework the sidebar components into a wider, calmer desktop navigation and a
  full-width phone drawer with one clear workspace menu, labelled creation,
  legible selected/waiting states, and deliberately contained metadata.
- Make transcript disclosures, tool statuses, detail labels, resource blocks,
  plans, and long technical content readable and width-safe; give the jump
  control a non-occluding home.
- Consolidate the composer into a responsive input surface with explicit
  submission/cancellation actions, labelled session controls, and readable
  context metadata; compact pending permission and elicitation cards without
  hiding their choices.
- Tighten settings cards and nested MCP groups, clarify authentication and
  registration actions, wrap support output, theme scrollbars, and add a served
  favicon.
- Add the responsive regression coverage, complete the remaining browser-flow
  verification, then remove the completed visual-design subtree from
  `eng/todo.md` after its completion review passes.
