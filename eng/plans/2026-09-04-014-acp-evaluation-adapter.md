# Add an ACP Evaluation Baseline

## Sources

- `eng/roadmap.md#acp-evaluation-adapter` — owns the task coverage, run
  metadata, isolation, budget, artifact, and fake-provider gates.
- `docs/spec.md#sessions-and-turns`, `#workspace-operations`, and
  `#session-lifecycle-and-recovery` — own the ACP behavior exercised by the task
  set.
- `eng/architecture.md#evaluation-boundary` and `#testing-boundaries` — place
  evaluation outside production semantics and require the shipped process and
  deterministic provider boundary.
- `internal/acp/types.go` and `internal/e2e/harness_test.go` — current wire
  vocabulary and process-driving patterns for initialization, sessions, streamed
  updates, permissions, cancellation, and fake OpenRouter responses.
- `~/src/references/repos/personal/beta/src/bin/coral-prompt.rs:run`,
  `render_update`, and `auto_approve`, with
  `tests/e2e.rs:coral_prompt_streams_the_answer_to_stdout` and
  `coral_prompt_approves_a_mutating_tool_call` — prior art for a one-shot ACP
  client that drives the real agent process and handles reverse permission
  requests.
- `~/src/references/repos/personal/eta/harbor/mer_agent/agent.py:run` and
  `populate_context_post_run`, plus
  `~/src/references/repos/personal/beta/evals/tasks/fix-anagram/` — prior art
  for fresh trial workspaces, durable artifacts, usage extraction, and
  task-owned verification.

## Goal

Add an evaluation-only ACP client and a versioned local task corpus. A run uses
a fresh workspace, measures the complete Ox process including delegated child
work, verifies external artifacts, and writes enough structured evidence to
reproduce and classify the result.

## Implementation

- `evals/cmd/ox-eval/` and focused packages under `evals/internal/` — add a
  small Go runner that loads a task, creates isolated workspace/config/data
  directories, starts the already-built `ox` executable, and performs the ACP
  initialize/new/prompt lifecycle over stdio. Handle `session/update` and
  `session/request_permission`, support allow and deny policies, enforce the run
  deadline with `session/cancel`, and support a declared stop/restart/load
  phase. Always wait for or terminate the process and retain stdout, stderr,
  sanitized trace, ACP events, verifier output, and the final workspace.
- Put the evaluator's provider gateway between Ox and either scripted SSE or the
  explicitly selected provider endpoint. Use it to enforce and record one
  request budget across parent requests, retries, compaction, and children;
  never persist authorization headers. Derive retry counts from gateway attempts
  versus traced logical provider requests.
- Define a checked-in manifest schema and tasks under `evals/tasks/v1/` for a
  single-file edit, coordinated multi-file change, repository navigation, long
  input, permission rejection, cancellation, and durable restart. Each task
  declares its prompt and phases, seed workspace, permission policy, budget, and
  objective verifier as expected file data or an argument-vector test command.
- Write one JSON result per repetition plus a run index. Record schema version,
  Ox Git revision, a content-derived task revision, nonsecret model/provider
  settings, budget, requested repetition count, success criteria, elapsed time,
  stop/failure classification, provider attempts and retries, and ACP token/cost
  data when present. Encode unavailable usage and cost as `null`.
- `Makefile` and `evals/README.md` — add explicit fake-smoke and on-demand run
  entry points. Keep evaluation out of `make check`; require an affirmative live
  flag before using the repository's authorized provider credential.
- `AGENTS.md` and `eng/architecture.md` — add the evaluation directory's
  test-only ownership and dependency direction without changing Ox's runtime
  boundary.

## Tests

- Unit-test manifest validation, safe fixture copying, verifier execution,
  permission selection, result serialization with unknown usage, task revision
  stability, and provider-attempt budgeting.
- The explicit fake-provider smoke target builds Ox, runs a scripted task
  through setup and prompt, captures artifacts, and proves successful teardown.
  Additional cases hold a response past the deadline and inject setup, protocol,
  provider, verifier, and process-exit failures so each receives a stable
  classification and no child process remains.
- Validate every checked-in task manifest and verifier without making a paid
  provider request.

## Decisions

- Port the prior art's adapter boundary, not Harbor itself. The local Go runner
  supplies the required ACP, isolation, budget, and artifact contracts without
  adding a Python/Docker framework dependency; its task and result formats stay
  independent of any benchmark host.
- Treat the verifier as authoritative. Final answer text is an artifact only and
  cannot make a failed workspace pass.
