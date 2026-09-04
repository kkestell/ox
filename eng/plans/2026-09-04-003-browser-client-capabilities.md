# Browser Client Capability Negotiation

## Sources

- `eng/roadmap.md#current-milestone-client-interoperability-baseline` — scope
  and acceptance gate for recording the pinned browser client's capabilities and
  checking omitted optional capabilities.
- `eng/architecture.md#protocol-boundary` and
  `eng/architecture.md#testing-boundaries` — capability negotiation and the
  independent-client test boundary.
- `formulahendry/acp-ui@e6e36d05:src/stores/session.ts`,
  `src/lib/acp-bridge.ts`, and `src/components/TrafficMonitor.vue` — the pinned
  browser build's capability request and its visible ACP traffic recorder.
- `internal/agent/agent.go#Agent.Initialize` and `internal/e2e/auth_test.go` —
  Ox's terminal-auth negotiation and the existing positive-capability case.

## Goal

Make the browser interoperability suite pin the ACP UI web client's actual
initialization capabilities and prove that Ox does not advertise terminal
authentication when the client omits that optional capability.

## Implementation

- `internal/e2e/browser/lifecycle.spec.ts` — add a focused Playwright case that
  opens ACP UI's traffic monitor, creates a session through the existing
  harness, and expands the visible outgoing and incoming `initialize` entries.
  Assert protocol v1 and the `acp-ui` client identity, plus the browser profile:
  `fs.readTextFile` and `fs.writeTextFile` are false while terminal and
  terminal-auth support are absent. Assert the initialize response contains the
  ordinary OpenRouter method but no terminal authentication method.
- `eng/roadmap.md` — mark the browser-capability slice complete. Keep the exact
  profile in the executable browser assertion and retain the existing client,
  bridge, and schema pins as the interoperability record.

## Tests

- The pinned, unmodified ACP UI must expose the exact request and negotiated
  response through its own traffic monitor while connected to the real Ox
  process.

## Decisions

- Observe traffic through ACP UI's shipped monitor rather than parsing or
  proxying ACP in the Ox harness. This keeps the independent-client boundary
  intact.
- Do not implement delegated filesystem or terminal execution here. Their use
  and unavailable-capability fallback belong to the client-delegated-tools
  milestone.
