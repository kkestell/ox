# Prove Local and Delegated Executor Conformance

## Sources

- `eng/roadmap.md#executor-conformance` — owns the required browser fallback,
  delegated process, permission, durable-record, and replay gates.
- `eng/architecture.md#workspace-boundary` and
  `eng/architecture.md#testing-boundaries` — define executor selection and the
  separate browser and process test boundaries.
- `internal/e2e/browser/harness.ts` and `internal/e2e/browser/lifecycle.spec.ts`
  — existing deterministic model queue, seeded workspace, visible tool-turn,
  traffic-monitor, and replay patterns.
- `formulahendry/acp-ui@e6e36d05:src/components/PermissionDialog.vue` and
  `src/components/ChatView.vue` — pinned client permission and tool-workflow UI.
- `internal/e2e/filesystem_test.go`, `internal/e2e/terminal_test.go`, and
  `internal/e2e/session_test.go` — delegated callback drivers, local process
  fallback, persistence, and `session/load` patterns.
- `internal/agent/state.go` — durable records whose executor-independent content
  must be preserved.

## Goal

Prove that read, exact-edit, and shell workflows have the same model-visible and
durable behavior through Ox's local and ACP-client executors. Exercise the
pinned browser client's visible permission flow on the local fallback path.

## Implementation

- `internal/e2e/browser/harness.ts` — add one scripted model workflow that reads
  a seeded file, edits it, runs a shell command, and returns a deterministic
  answer. Expose focused fixture inspection needed to assert the local file and
  shell effects without interpreting ACP traffic in the harness.
- `internal/e2e/browser/lifecycle.spec.ts` — drive that workflow through ACP UI,
  answer the edit and shell permission requests through the visible permission
  dialog, and assert the completed tool cards, answer, and workspace effects.
  Use the traffic monitor to retain proof that the pinned web client advertises
  no delegated filesystem or terminal capability and receives no delegated
  method call.
- `internal/e2e` — add one shared deterministic read/edit/shell scenario run
  once without optional capabilities and once with filesystem and terminal
  capabilities. Service delegated callbacks from client-owned file and terminal
  state, while the local case uses the process workspace, and assert equal tool
  results and final effects.
- `internal/e2e` persistence helpers — close each session, restart Ox, load it,
  and compare the complete ordered replay transcript and session log across
  executors after normalizing only generated session/message/turn identities,
  timestamps, and temporary roots. Keep tool-call identities, results,
  permission decisions, statuses, and ordering exact.
- `eng/roadmap.md` — replace the completed client-interoperability summary with
  a concise client-delegated-tools summary, remove that milestone's detailed
  scope, and make durable session completion current. Keep ACP method coverage
  synchronized with the now-complete executor behavior.

## Tests

- The pinned ACP UI completes local read, exact-edit, and shell operations,
  presents and resolves both mutation permissions, and shows successful tool
  completion without an agent-to-client filesystem or terminal callback.
- The real-process harness completes the same workflow with delegation enabled,
  proves client-owned state changed instead of the local files or shell, and
  observes the same model-visible tool results as the local executor.
- Normalized durable records and `session/load` updates are equal between the
  two executors, including permission decisions and tool-call lifecycles.

## Decisions

- Use one workflow and one expected transcript for both executors so the test
  cannot hide drift behind executor-specific assertions. Normalize only values
  that are intentionally minted per run; capability selection itself must not
  appear in the durable record until request-configuration capability recording
  is implemented.
