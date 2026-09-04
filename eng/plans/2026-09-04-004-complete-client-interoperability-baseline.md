# Complete Client Interoperability Baseline

## Sources

- `eng/roadmap.md#current-milestone-client-interoperability-baseline` —
  remaining browser rendering, error, stdout-isolation, and pin-recording gates.
- `eng/architecture.md#protocol-boundary` and
  `eng/architecture.md#testing-boundaries` — ACP notification ordering and the
  pinned, unmodified browser-client boundary.
- `internal/agent/adapter.go`, `internal/agent/state.go#replay`, and
  `internal/acp/types.go` — live and replayed updates and the prompt content
  capabilities Ox advertises.
- `internal/e2e/browser/harness.ts` and `internal/e2e/browser/lifecycle.spec.ts`
  — current browser process fixture, fake provider, traffic-monitor assertions,
  and visible lifecycle coverage.
- `formulahendry/acp-ui@e6e36d05:src/stores/session.ts` and
  `src/components/ChatView.vue` — the pinned client submits text prompts and
  renders text user/agent messages, thoughts, and tool-call status changes.
- `~/src/references/repos/third-party/protocol/agent-client-protocol@8e3eb8f2:schema/v1/schema.json`
  — pinned ACP v1 content-block and session-update wire contract.

## Goal

Finish the client interoperability baseline with one deterministic browser turn
that exercises every visible update supported by the pinned ACP UI, plus a
browser-visible protocol error and evidence that Ox logging stays off the ACP
stream. Record the exact client, bridge, and schema pins when closing the
milestone.

## Implementation

- `internal/e2e/browser/harness.ts` — extend the fake provider queue just enough
  to script a reasoning delta, a local read-tool call, the follow-up answer, and
  usage. Seed the read target in the isolated workspace. Keep separate captured
  bridge diagnostics and expose focused wait/access helpers needed to prove Ox
  logs reached stderr while the browser connection remained valid.
- `internal/e2e/browser/lifecycle.spec.ts` — add one UI-driven turn that asserts
  the text prompt, streamed thought, tool title and
  pending/in-progress/completed lifecycle, and final Markdown answer. Disconnect
  and resume the saved session to prove `user_message_chunk` and the same
  assistant/tool transcript render correctly from Ox replay. Use ACP UI's
  traffic monitor to assert Ox advertises text/resource-link baseline support
  plus image, audio, and embedded-context support, and that the emitted usage
  update reaches the client even though this ACP UI build has no usage display.
- `internal/e2e/browser/lifecycle.spec.ts` and `harness.ts` — run Ox at debug
  log level, submit an absolute but invalid workspace through the visible
  session form, and assert the UI presents Ox's useful JSON-RPC error. Assert
  debug startup/session logs are present in captured stderr diagnostics, absent
  from browser ACP traffic, and do not produce a browser transport parse
  failure.
- `eng/roadmap.md` — replace the previous completed-milestone summary with a
  concise client-interoperability summary containing the ACP UI artifact
  `4482f93a` (source `e6e36d05`), `@rebornix/stdio-to-ws@0.2.0`, and ACP schema
  `8e3eb8f2`; remove the completed milestone detail and make client-delegated
  tools current.

## Tests

- The pinned ACP UI visibly renders a live and replayed text/thought/tool/answer
  transcript driven through the real Ox process, including every tool status.
- The traffic monitor observes advertised prompt variants and `usage_update`
  without malformed or log-contaminated inbound traffic.
- A rejected `session/new` reaches the UI as a specific, readable error while Ox
  debug logs remain confined to stderr diagnostics.

## Decisions

- Do not inject ACP messages or patch the upstream client. ACP UI web 0.1.15
  exposes only text prompt submission and does not display non-text user blocks,
  tool output content, or usage. Exercise its supported visible surface here;
  keep the broader Ox content contract at the protocol tests and canonical ACP
  schema identified by `eng/architecture.md`.
