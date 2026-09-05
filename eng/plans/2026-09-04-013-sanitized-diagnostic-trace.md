# Emit a Sanitized Diagnostic Trace

## Sources

- `docs/spec.md#diagnostic-trace` — owns trace activation, file lifecycle,
  failure behavior, event coverage, and the privacy allowlist.
- `eng/roadmap.md#sanitized-trace` — owns the slice scope and completion gates.
- `eng/architecture.md#system-boundary`, `#responsibilities`, and
  `#session-and-turn-state` — own stdout isolation, package boundaries, turn
  concurrency, and recovered-turn behavior.
- `cmd/ox/main.go`, `internal/agent/agent.go`, `internal/agent/loop.go`,
  `internal/agent/compact.go`, and `internal/agent/subagent.go` — current
  process startup and the live turn, provider, tool, permission, compaction,
  subagent, cancellation, and recovery seams.
- `~/src/references/repos/personal/beta/src/trace.rs:Trace::{off,to_sink,turn,emit}`
  and tests `every_top_level_event_carries_the_root_id_and_timestamp`,
  `an_off_trace_and_its_derived_subagent_emit_nothing`, and
  `a_turn_trace_stamps_a_fresh_run_id_and_the_session_id` — prior art for a
  disabled no-op sink, synchronized JSONL emission, timestamping, and
  correlation across interleaved sessions.
- `~/src/references/repos/personal/beta/src/main.rs:main` and
  `src/acp.rs:run_turn` — prior art for process wiring and deriving one scoped
  trace per ACP turn.

## Goal

Add an opt-in `--trace <path>` diagnostic file that reconstructs each live
turn's shape without recording any user, model, workspace, shell, or credential
content.

## Implementation

- `internal/trace/trace.go` — add a concrete, concurrency-safe JSONL sink with
  disabled and file-backed forms. Open the requested path with create/truncate
  semantics, serialize one complete versioned object while holding the writer
  lock, and derive turn scopes carrying `session_id` and `turn_id`. Expose
  narrow lifecycle methods rather than accepting arbitrary maps or payload
  strings. Events cover turn start/completion, provider request
  start/completion, tool pending/start/completion, and permission
  request/decision. Use enumerated kinds and outcomes, timestamps and elapsed
  milliseconds, request counts, token or byte counts, tool names, stop reasons,
  and correlation IDs only. A write failure closes and disables the sink and
  invokes one stderr-reporting callback.
- `cmd/ox/main.go` — accept `ox --trace <path>` for server mode, open the trace
  before agent startup, fail startup when it cannot be created, close it during
  shutdown, and pass it through `agent.Config`. Keep `login` separate and update
  usage errors for malformed flag combinations.
- `internal/agent/agent.go`, `loop.go`, `compact.go`, and `subagent.go` — derive
  a trace after a prompt or recovered turn claims the session, then instrument
  the existing lifecycle seams. Classify provider requests as primary,
  compaction, or subagent work; correlate subagent work with its parent
  tool-call ID; wrap the permission callback once so both normal and recovered
  approvals emit the same events; and record tool outcomes without arguments,
  titles, paths, raw errors, or output. Finish every claimed trace with the
  durable turn outcome or sanitized failure/cancellation outcome. Trace only
  live activity; do not synthesize events while replaying durable history.
- `internal/e2e/harness_test.go` — let process tests supply command arguments so
  they can start the real binary with a trace path.
- `AGENTS.md`, `eng/architecture.md`, and `eng/roadmap.md` — add the trace
  package to the codebase map and dependency ownership, describe the lossy live
  diagnostic boundary, and collapse the completed context-and-diagnostics
  milestone after its gates pass.

## Tests

- `internal/trace/trace_test.go` — prove disabled tracing is a no-op, every line
  is independently valid JSON with the required version and correlation fields,
  concurrent writers cannot interleave records, and one injected write failure
  reports once and disables later writes.
- `internal/e2e/trace_test.go` — run a real prompt through multiple provider
  requests, a permission, and a shell tool. Assert the ordered event shape,
  session/turn/tool correlations, kinds, timings, sizes, outcomes, and stop
  reason. Put distinct sentinels in the credential, prompt, reasoning, model
  text, tool arguments, and shell output and assert none occur in the trace.
  Exercise cancellation plus subagent and compaction provider requests so their
  outcomes and parent correlation are visible without their content. The same
  run proves stdout remains parseable ACP traffic.
- `internal/e2e/trace_test.go` and existing command-usage coverage — prove the
  flag replaces an existing file, an unavailable path fails before serving,
  malformed flag forms remain usage errors, and omitting the flag creates no
  trace artifact.

## Decisions

- Port Beta's scoped sink and correlation pattern, not its trace schema. Omit
  Beta's process-only events and its assistant text, result text, tool
  arguments, tool output, raw errors, configuration, and compaction text. Ox's
  stricter trace contains only the specification's allowlisted metadata and
  writes to the requested file rather than stderr.
- Represent subagent activity with the invoking ACP tool-call identity instead
  of Beta's separate subagent envelope. Ox already assigns public tool-call IDs
  to delegated work, so this preserves one correlation vocabulary across ACP,
  durable state, and the trace.
