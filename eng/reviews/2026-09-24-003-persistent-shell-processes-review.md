# Persistent shell processes review

## Scope and coverage

Reviewed the entire working-tree code diff against `HEAD` before the fixes,
including the new `src/shell_processes.rs`, the manifest, tests, and careful
example hook changes. Followed callers through the tool boundary, prompt
execution, permission requests, session activation and deletion, ACP shutdown,
and headless runs. Read the persistent shell process plan and changed
documentation as requirements. No changed code was excluded based on earlier
reviews.

Selected lenses: correctness, resources, concurrency, error handling, security,
API design, architecture, performance, testing, readability, documentation,
Rust ownership, Rust idioms, and Cargo. Inspected the installed
`agent-client-protocol` 2.1.0 implementation to check the connection lifetime
and output delivery guarantees.

Validation used local HTTP and ACP fixtures and real subprocesses on macOS.
No live OpenRouter request, interactive ACP client walkthrough, or Linux run
was performed. Focused reproductions were added only to a temporary source
copy; repository source code was not modified by this review.

## Findings

### Medium

#### Correctness

- **Shutdown can discard queued ACP responses** (`src/acp.rs:980`): The new
  `connect_with` foreground future returns as soon as operation guards have
  been released. At that point a prompt's final response has been queued, but
  the transport need not have written it. Returning drops the connection and
  its pending output. A client that closes its input while continuing to read
  responses can therefore lose the final response when its output transport
  is slow. The previous `connect_to` path explicitly awaited outgoing queue
  draining and transport completion; `connect_with` does neither here.

  A focused reproduction supplied an initialization and prompt request,
  closed incoming input, and held the final response behind a gate in the
  outgoing sink. `serve` returned `Ok(())` while the sink was blocked, and the
  response was never delivered. Restoring `connect_to` in the temporary copy,
  with operation shutdown awaited in the close callback, made the same check
  pass: serving remained pending until the gate opened, then delivered the
  response. The existing EOF tests use an always-ready unbounded sink and do
  not exercise this case.

  Preserve the previous output-draining guarantee while adding signal
  handling, or await an equivalent transport completion barrier after the
  operations finish. The dependency's `drain_outgoing` method is private, so
  it cannot simply be called from this closure. Add a regression check with a
  blocked outgoing sink.

### Low

#### Resources

- **Finished shell processes retain their stdin pipe**
  (`src/shell_processes.rs:163`): `start` removes `ChildStdin` from the child
  and stores it in `ShellProcess`, but the supervisor has no access to that
  handle. Its terminal cleanup therefore leaves the pipe open until the last
  process handle is dropped, normally after registry eviction or session
  removal. Up to 16 unused file descriptors can remain per active session.

  A focused reproduction started `true`, waited for its final state, and
  observed that the stored stdin still contained a live file descriptor. A
  subsequent write returned `Interruption::Finished` with
  `stdin_closed: false`, rendering as “stdin remains open” despite the command
  having ended. Awaiting owner shutdown does not clear this stored handle.

  Close the stored stdin as part of terminal cleanup and report it as closed
  for later writes. Coordinate this with an in-flight writer while preserving
  bounded cleanup. Cover natural exit without an explicit stdin closure;
  changing only the reported boolean would leave the descriptor retained.

## Checks run

- `cargo fmt --all -- --check`: passed.
- `cargo test --all-targets --all-features`: passed, 145 tests.
- `cargo build --all-features`: passed.
- `cargo clippy --all-targets --all-features -- -D warnings`: passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: passed, 1 test.
- `python3 -m unittest discover -s examples/skills/careful/scripts`: passed, 4 tests.
- `git diff --check`: passed.
- Temporary reproduction
  `review_repro_eof_waits_for_final_response_to_flush`: failed on the reviewed
  implementation because the final response was lost; passed with the
  previous draining connection path restored as an experimental control.
- Temporary reproduction `review_repro_natural_exit_releases_stdin`: failed
  on the reviewed implementation, confirming the retained descriptor and
  `stdin_closed: false` result.

## Verdict

Both findings were fixed before the feature commit. ACP shutdown now drains
accepted responses through the physical transport; the supervisor closes
stdin before reporting a finished command. The two new regression tests and
all 147 Rust tests pass, as do formatting, build, Clippy, and both example
test suites.
