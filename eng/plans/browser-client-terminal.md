# Browser client terminal callbacks

## Goal

Implement the ACP terminal callback lifecycle in the first-party browser client
so Ox can delegate shell commands to the selected workspace through the Bun
host.

## Desired outcome

The host advertises terminal support only after it can create a confined command
process, return bounded incremental output and its terminal status, wait or kill
the process, and release all retained state. Ox shell calls use that lifecycle
through a real browser-hosted process.

## Summary of approach

Add a session-scoped terminal executor beside the filesystem executor. It starts
the exact requested executable and argument vector in a validated workspace
directory, supplies the requested environment, tracks each child by a random
identifier, and maintains a character-safe tail buffer with an optional ACP byte
limit. The executor routes create, output, wait, kill, and release through the
supervisor's ACP callbacks; terminal capability negotiation is enabled with
those handlers. Tests cover the executor state machine and browser harness
scenarios driven by real Ox shell calls.

## Related code

- `client/src/workspace-supervisor.ts` - owns ACP capability advertisement and
  routes client callbacks for a workspace's active sessions.
- `client/src/filesystem-executor.ts` - established shape for a confined,
  session-scoped host executor.
- `client/src/terminal-executor.ts` - terminal process lifecycle implementation.
- `client/src/terminal-executor.test.ts` - stable unit coverage for process and
  terminal-state behavior.
- `client/e2e/smoke.spec.ts` - real Ox/browser conformance scenarios.
- `internal/tools/shell.go` - agent-side delegated shell sequence and cleanup
  contract exercised without changing it.
- `eng/client-architecture.md` - owns the terminal executor boundary.
- `eng/todo.md` - records this browser-client milestone task.

## Current state

- The supervisor already confines filesystem callbacks by active session but
  does not register terminal handlers or advertise `terminal` capability.
- Ox's delegated shell tool creates `/bin/sh -c` with an absolute workspace CWD,
  waits, reads output, and releases; on cancellation or timeout it kills before
  reading output and releasing.
- The SDK supplies complete typed ACP terminal callback request and response
  shapes.

## Structural considerations

- **Hierarchy:** `WorkspaceSupervisor` continues to own routing; the terminal
  executor owns only workspace child processes and their retained state.
- **Abstraction:** one concrete executor directly models ACP terminals; no
  browser-facing shell API or generic process service is introduced.
- **Modularization:** process launch, output collection, and lifecycle lookup
  remain together because they share terminal ownership.
- **Encapsulation:** terminal IDs stay host-generated and session-bound; no
  browser snapshot exposes a process handle or arbitrary execution operation.
- **Testability:** deterministic executable fixtures exercise the executor,
  while browser tests prove the advertised capability works with real Ox calls.

## Test plan

- **Key behaviors to verify:** exact argv, supplied environment, workspace CWD,
  active-session ownership, incremental output, UTF-8-safe bounded tail output,
  normal and signal exit status, wait, kill, release, and cancellation.
- **Test levels:** focused Bun executor tests plus Playwright tests that send Ox
  shell requests through the actual host.
- **Edge cases and failure modes:** invalid or escaped CWD, invalid output
  limits or environment entries, unknown/cross-session/released IDs, failed
  spawn, a command that outputs more than its limit, cancellation of a process
  tree, and idempotent allowed cleanup calls.
- **What not to test:** browser terminal presentation, interactive stdin, or
  agent shell implementation internals.

## Implementation plan

- Add the terminal executor with confined CWD validation, process-group-aware
  launch and termination, bounded output collection, exit completion, and ACP
  lifecycle methods.
- Route every terminal callback in the supervisor and advertise terminal
  capability alongside the completed handlers.
- Add focused executor tests and real browser/Ox shell scenarios, then mark the
  roadmap item complete.

## Documentation updates

- Mark the implemented terminal callback task in `eng/todo.md`.

## Impact assessment

- Code paths affected: browser-host ACP callbacks and Ox delegated shell calls.
- Data, protocol, or schema impact: no protocol changes; the existing terminal
  capability becomes truthfully advertised.
- Dependency or API impact: no new dependency or browser-facing API.

## Validation

- Run the client type and unit gates, relevant Playwright coverage, and the
  repository's required behavior gate after integration.
