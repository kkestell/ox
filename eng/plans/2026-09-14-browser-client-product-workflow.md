# Browser client product workflow

## Goal

Replace the browser's method-by-method ACP surface with the conversation product
defined in `eng/client-architecture.md`, without weakening its ACP coverage or
adding CSS.

## Desired outcome

Opening the client shows the newest conversation, or a new empty conversation
when no history exists. New and history selection are direct actions; Resume is
absent. Model, mode, reasoning, and context usage are on the transcript view.
MCP servers and support information are secondary workspace settings, and an
unfinished MCP draft cannot block any conversation action.

## Summary of approach

Give the private browser protocol product-intent conversation commands and let
the host choose the required ACP operation. Keep the current MCP server set in
the Bun host and change it only through an explicit validated command. Recompose
the React tree around history, one selected transcript, contextual interactions,
and the composer, using only semantic HTML and native controls. Preserve
complete ACP coverage in focused unit tests and real-process Playwright
workflows.

## Related code

- `client/src/{protocol,host,workspace-supervisor}.ts` — product commands,
  default selection, MCP server state, and ACP routing.
- `client/src/browser.tsx` — unstyled conversation product surface.
- `client/e2e/smoke.spec.ts` — real Ox product and reconnect workflows.
- `eng/client-architecture.md` and `docs/spec.md#web-client` — the approved
  architecture and observable behavior.

## Current state

- The host already owns Ox, active session controllers, callbacks, transcripts,
  and browser-safe snapshots.
- The browser exposes ACP lifecycle methods and capability projections as peer
  controls; its MCP form validation is shared by unrelated session actions.
- The functional-gate Make, documentation, and test changes are present in the
  worktree but must be rerun after the product rewrite.

## Structural considerations

- **Hierarchy:** React submits product intent; the host remains the only ACP and
  process owner.
- **Abstraction:** Conversation opening is one operation above load/select;
  resume remains protocol coverage rather than product navigation.
- **Modularization:** Keep the existing protocol, supervisor, controller, and
  React boundaries; add no UI framework or generic settings system.
- **Encapsulation:** MCP secret values and support diagnostics never enter the
  primary transcript or browser snapshots.
- **Testability:** Unit tests prove routing and state ownership; Playwright uses
  accessible product labels against the real Bun host and Ox process.

## Test plan

- Prove authenticated startup loads the newest durable conversation and creates
  one only when history is empty.
- Prove one history action selects an active conversation or loads an inactive
  one with replay, and no user-facing Resume command exists.
- Prove New creates and selects a distinct session; close and delete remain
  selected-conversation actions; paging extends one history list.
- Prove an invalid browser-local MCP draft cannot block New or history, while an
  explicitly valid server set is used by later activations without echoing
  header or environment secrets.
- Prove model, mode, reasoning, and context usage render with the selected
  transcript; workspace settings contain only MCP servers and support details.
- Prove attachments are behind Add context, plans render only when present, tool
  details are expandable, and raw IDs, revision, status, and stderr are absent
  from the primary surface.
- Retain real-process coverage for authentication, prompting, cancellation,
  concurrent sessions, replay, permissions, forms, filesystem, terminal, MCP,
  process failure, refresh, and multiple browsers.

## Implementation plan

- Replace lifecycle-shaped browser commands with New, open-conversation,
  history-page, selected-conversation close/delete, and explicit MCP-server-set
  commands; update validation and host dispatch tests.
- Make the supervisor open the newest conversation after authentication, create
  the empty-history default, select active controllers, load inactive history,
  and apply the host-owned MCP server set to later activations.
- Recompose `browser.tsx` to match the approved semantic product surface. Keep
  form drafts component-local and never consult MCP form validity from session
  navigation.
- Rewrite Playwright selectors and scenarios around product outcomes, add the
  default-session and invalid-MCP-draft regressions, and retain the complete ACP
  real-process matrix.
- Run the focused client gates, inspect for any CSS or protocol terminology in
  the primary UI, then execute the existing functional-gate plan and completion
  review before marking the milestone complete.

## Impact assessment

- Code paths affected: private browser protocol, host dispatch, supervisor
  session activation, React presentation, and browser tests.
- Data or protocol impact: the private browser protocol changes; ACP v1 and Ox
  session storage do not.
- Dependency impact: none.

## Validation

- Run `bun run check`, `bun test`, `bun run build`, and `bun run test:e2e` from
  `client/`.
- Run `make check-docs`, then the repository client gate and `make check-all`
  during the functional-gate task.
