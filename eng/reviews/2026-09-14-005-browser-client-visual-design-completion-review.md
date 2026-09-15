# Browser client visual-design completion review

## Scope and mode

General review of the completed responsive browser-client visual-design
milestone, including its React surfaces, vendored UI primitives, generated-style
toolchain, browser host asset serving, product-surface documentation, and
Playwright coverage.

Selected topics:

- **Correctness** — the milestone promises width-safe phone and desktop
  rendering and complete navigation between conversation and settings states.
- **Testing** — the change repairs visual regressions and adds geometry checks
  that must distinguish the affected content variants and viewport boundaries.
- **Readability** — most presentation behavior moved into component structure
  and utility classes, so local ownership and repeated layout rules matter.
- **Architecture** — the change replaces the appearance implementation and
  control primitives while preserving the existing host/browser boundary.
- **Security** — credential and MCP-secret forms changed presentation and must
  retain the browser-safe snapshot boundary.
- **Dependencies** — Tailwind, Radix, shadcn, and icon tooling were added to the
  client package.

## Findings

### [P2] Settings can lose their only explicit return action

`client/src/components/app.tsx:210` renders the settings-header back button only
when `active` exists. The unauthenticated state deliberately has no active
conversation, and an unavailable workspace can also have none, so opening
settings from either actionable empty state produces a settings page with no way
back in its header. This violates the plan's explicit return-action decision and
is especially awkward on a phone, where the only escape is reopening the
navigation drawer. Always render the return button and name its destination for
the current state (for example, “Back to workspace” without an active
conversation); add the unauthenticated settings transition to the browser test.

### [P2] Several transcript variants can still widen the primary surface

`client/src/components/transcript.tsx:98`,
`client/src/components/transcript.tsx:169`, and
`client/src/components/transcript.tsx:188` leave plan content, terminal and
unknown tool output, and resource-link paragraphs at normal word-breaking.
Unbroken agent-provided plan text, terminal identifiers, resource names or
descriptions can therefore exceed a 390px transcript even though ordinary text,
paths, and preformatted output are contained. Apply the same `min-width: 0` and
`overflow-wrap: anywhere` rule to every transcript content variant. Extend the
responsive fixture beyond its current long user/tool-title/assistant text so a
long resource link or plan item and long support diagnostic prove these paths.

## Topic verdicts

- **Correctness:** two user-visible completeness gaps remain: return navigation
  in settings without an active session and width containment for less common
  transcript variants.
- **Testing:** the new phone drawer, header alignment, jump-row, pending-tray,
  composer-action, and basic overflow checks are useful; variant-specific long
  content and unauthenticated settings return are not yet covered.
- **Readability:** nothing else to report. Region components and small shared
  presentation helpers keep the layout rules locally understandable.
- **Architecture:** nothing to report. React remains the sole presentation owner
  and the client continues to consume browser-safe host snapshots.
- **Security:** nothing to report. Credential and MCP secret values remain
  write-only and the browser tests still assert they do not reach the primary
  surface or provider transcript.
- **Dependencies:** nothing to report. The exact-version client dependencies
  directly support the settled Tailwind/shadcn implementation; shadcn remains a
  development-only vendoring tool.

## Checks and unresolved validation

- `bun run test`: 94 tests passed.
- `bun run check`: Tailwind build and TypeScript checking passed.
- `bun run test:e2e`: 19 of 20 scenarios passed; the remaining credential-form
  assertion was corrected and its focused scenario then passed. The complete
  Playwright suite still needs to be rerun after review fixes.
- Focused responsive scenarios at 390px, 800px, and 1280px passed.

No suspicion requires evidence outside the repository. The two findings should
be fixed before removing the milestone from `eng/todo.md`.
