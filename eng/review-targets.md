# Review targets

Review the targets in order. Each production source file has one primary target.
Review the corresponding focused tests with that target, rather than as a
separate pass. The final target checks that the aggregate test suite proves the
contracts reviewed earlier.

## Process and protocol boundary

**Process inputs and observability:** `cmd/ox/`, `internal/credentials/`,
`internal/settings/`, and `internal/trace/`.

Review startup, configuration precedence, login and logout, secret handling,
standard-output purity, and trace privacy.

**ACP wire contract:** `internal/acp/`, plus `Agent.Methods`, initialization,
and cancellation handlers in `internal/agent/agent.go`.

Review ACP schema fidelity, JSON-RPC errors, input validation, and negotiated
capabilities.

## Session activation and persistence

**Session activation:** activation in `internal/agent/agent.go`, `prompt.go`,
`config_options.go`, and `tool.go`, plus `internal/skills/`.

Review new and resumed activation, frozen inputs, tool availability, root
instructions, skills, and configuration options.

**Durable session format:** `internal/agent/state.go` through compaction
application, plus `store.go` and `lock*.go`.

Review JSONL framing, append and sync ordering, checkpoints, corruption
handling, and session deletion.

**Recovery and replay:** `internal/agent/agent.go` and the execution,
suspended-exchange, and replay sections of `internal/agent/state.go`.

Review restart behavior, uncertain effects, pending permission recovery, and the
replayed transcript.

## Prompt execution

**Prompt and updates:** prompt entry in `internal/agent/agent.go`, the request
and history portions of `loop.go`, `adapter.go`, and `event.go`.

Review content conversion, streamed updates, conversation history, cancellation,
usage, and stop reasons.

**Tool dispatch and permission:** the dispatch portion of
`internal/agent/loop.go`, `approval.go`, `reads.go`, and tool registration in
`internal/tools/tools.go`.

Review batch ordering, parallel reads, serialized effects, permission grants,
refusals, and durable dispatch intent.

**Context admission and compaction:** `internal/agent/compact.go` and its
state-record handling.

Review context sizing, protected message groups, summaries, and atomic failure
or cancellation behavior.

## Workspace and built-in tools

**Workspace confinement:** `internal/workspace/`.

Review canonical roots, symlink-race resistance, ignored traversal, bounded
output, and spills.

**File and search tools:** `internal/tools/{read,write,edit,glob,grep,text}.go`.

Review read evidence, atomic edits, text preservation, local and delegated
executor parity, and spill safety.

**Shell execution:** `internal/tools/shell.go` and `internal/shellrules/`.

Review the sanitized environment, process groups, cancellation, delegated
terminal cleanup, and reusable shell grants.

**Stateful interaction tools:**
`internal/tools/{question,todo,memory,skill,task}.go`,
`internal/agent/{memory,task_queue}.go`, and the todo/task state sections of
`internal/agent/state.go`.

Review elicitation, plans, durable memory, skill loading, task transitions, and
replay.

**Web retrieval:** `internal/tools/web_fetch.go` and `web_fetch_oxe2e.go`.

Review URL and address policy, redirects, extraction, bounds, cancellation, and
spills.

## External services

**OpenRouter boundary:** `internal/openrouter/`.

Review request assembly, SSE framing, retries, the model catalog cache, partial
streams, and provider errors.

**MCP integration:** `internal/mcp/` and `internal/agent/mcp.go`.

Review activation atomicity, stdio and HTTP transports, redaction, tool
identity, shutdown, and recovery.

**LSP integration:** `internal/lsp/`.

Review process lifecycle, framing, document synchronization, UTF-16 position
translation, confined results, and deadlines.

## Verification and evaluation

**Test harness and coverage map:** `internal/e2e/` and `integration/`.

Review the harness and test matrix. Pair each focused test file with the target
above that owns its behavior. Confirm that every target has focused tests and,
where appropriate, an integration or shipped-binary test.

**Evaluation harness:** `evals/`.

Review the external ACP client, task loading, provider gateway, runner budgets,
verifiers, and fixtures. Keep the evaluation harness separate from production
agent semantics.
