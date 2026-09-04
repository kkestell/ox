# Delegate Shell Execution to ACP Client Terminals

## Sources

- `eng/roadmap.md#delegated-terminal` — required executor selection, terminal
  lifecycle, cancellation, output, error, fallback, and process-level gates.
- `eng/architecture.md#workspace-boundary` and
  `eng/architecture.md#testing-boundaries` — ownership of shell safety, client
  delegation, durable results, and interoperability coverage.
- `internal/agent/agent.go`, `internal/agent/loop.go`,
  `internal/agent/subagent.go`, and `internal/agent/tool.go` — negotiated client
  state and the executor seam shared by primary and delegated tool loops.
- `internal/tools/shell.go`, `internal/workspace/stream.go`, and
  `internal/workspace/spill.go` — current shell validation, sanitized
  environment, timeout, process cancellation, output rendering, and bounds that
  the delegated path must preserve where the ACP terminal contract permits.
- `~/src/references/repos/third-party/protocol/agent-client-protocol@8e3eb8f2:docs/protocol/v1/terminals.mdx`
  and `schema/v1/schema.json` — authoritative terminal capability, method
  ordering, request, exit-status, truncation, kill, and release contract.
- `~/src/references/repos/third-party/protocol/acp-go-sdk:agent_gen.go`,
  `types_gen.go`, and `json_parity_test.go` — typed agent-to-client callbacks
  and wire-parity patterns. No personal reference implements ACP terminal
  delegation; keep Ox's local runner as the fallback instead of porting another
  process abstraction.

## Goal

Execute the shell tool through ACP terminal callbacks when the client advertises
`terminal`, while keeping permission, rule grants, timeout semantics, bounded
model output, durable tool results, and the existing local fallback in Ox.

## Implementation

- `internal/acp/types.go`, `internal/acp/validate.go`, and ACP type tests — add
  constants and pinned v1 types for `terminal/create`, `terminal/output`,
  `terminal/wait_for_exit`, `terminal/kill`, and `terminal/release`, including
  environment entries, nullable exit code and signal, and truncation. Validate
  required outbound identifiers, command, absolute working directory, and
  positive byte limit; accept empty terminal output as valid.
- `internal/agent/agent.go`, `internal/agent/tool.go`, `internal/agent/loop.go`,
  and `internal/agent/subagent.go` — retain the negotiated terminal flag from
  `initialize`, construct cancellable callbacks for the five methods, and pass
  one terminal operation set through primary and subagent invocations. Do not
  persist the capability in request configuration; the roadmap assigns that to
  negotiated-capability recording.
- `internal/tools/shell.go` — select the client executor only when the complete
  terminal capability is available. Create `/bin/sh -c <command>` in the
  canonical session root with Ox's sanitized environment and
  `workspace.CollectionLimitBytes`, wait under the tool timeout, fetch output,
  and release exactly once after a terminal ID is returned. On timeout or turn
  cancellation, use a bounded cleanup context to kill before release. Attempt
  release after every later callback error, preserve the first lifecycle error,
  and let it fail only the tool call. Feed retained output through Ox's stream
  renderer and report the exact exit code or signal plus client truncation to
  the model; keep the existing local process-group path when delegation is
  unavailable.
- `eng/architecture.md#workspace-boundary` — replace the temporary statement
  that terminal execution is local with the implemented capability-selected
  executor boundary.

## Tests

- Add JSON parity and validation cases for all five methods, including omitted
  optional fields, empty output, nullable exit status, and empty success
  responses.
- Extend shell tool and agent-loop tests with a fake terminal to prove command,
  cwd, environment and byte-limit mapping; numeric and signalled exits;
  truncation; timeout and cancellation ordering; one release after create; each
  callback-error path; continued turns; permission and rule handling; and local
  fallback.
- Add real-process `internal/e2e` coverage for delegated success and
  cancellation, asserting `create -> wait/output -> release` and
  `create -> kill -> release`, a cancelled durable tool result, and replayable
  history. The pinned browser advertises no terminal capability, so its existing
  smoke suite remains the local-fallback acceptance case for this slice.
