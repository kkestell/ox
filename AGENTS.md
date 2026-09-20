THIS DOCUMENT MUST BE KEPT UP TO DATE

# Source Map

- `src/main.rs`: Command parsing and process entry; starts the ACP server or runs a credential command.
- `src/auth.rs`: Environment and operating-system keyring credential storage.
- `src/openrouter.rs`: OpenRouter client, request encoding, and streamed-response assembly.
- `src/tools.rs`: Concrete tool schemas, display titles, and execution of one complete call.
- `src/sessions.rs`: Transcript types, assistant-batch validation, `SessionStore` over one SQLite connection, and the private row codec.
- `src/acp.rs`: Connection wiring, `ServerState`, lazy OpenRouter client, and request handlers.
- `src/acp/operations.rs`: One active prompt, load, or delete per session, enforced by an operation guard.
- `src/acp/prompt.rs`: One prompt run: save the user message, request model output, run tools, save complete assistant batches, and respond.
- `src/acp/convert.rs`: ACP input conversion, session update construction, and transcript replay.

## Testing

Use the `OPENROUTER_API_KEY` in `.env` when testing to avoid keychain prompts.

## Backwards Compatibility

Currently, there is none. Delete and recreate `~/.local/share/ox/ox.db` rather than introducing migrations, versions, etc.

## Comments and Documentation

- Always describe things directly, clearly, and plainly
- Follow big idea up front and progressive disclosure
- Never use jargon, invented terms, or shorthand
- Never not mix definitions or overload terms

## Naming

- Use transcript for the durable conversation and transcript entry for one
  element. Do not introduce history, record, or event as domain synonyms.
- Use model request for one OpenRouter invocation. Reserve completion for the
  validated result of that request.
- Qualify client as ACP, OpenRouter, or HTTP whenever the surrounding type does
  not make it obvious.
- Describe state changes directly: saved, validated, running, completed,
  uncommitted, or cancelled. Avoid unqualified accepted, pending, and terminal.
- Say send an ACP update. Do not imply confirmed delivery or receipt.
- Say acquire or drop an operation guard. Avoid admission, claim, ownership,
  membership, and release for this mechanism.
- Keep external names such as `cwd`, `reasoning_details`, `AgentThoughtChunk`,
  and `MaxTurnRequests` at their protocol boundaries.

## Just Enough Rust

> Make things as simple as possible, but not simpler.

This is an experiment. Optimize for code that is cheap to change, not robust to
operate. Keep the domain behavior correct; keep the Rust implementing it thin,
boring, and easy to replace. Minimize committed surface area. When in doubt,
do less.

Default to the simplest thing that compiles and reveals whether the idea works.
Leaving a `// TODO:` or a `todo!()` for an unneeded case is better than
building speculative hardening around a design that is still moving.

### Correctness and robustness

- Use safe Rust. Keep the compiler-provided guarantees: no undefined behavior,
  data races, or type confusion.
- Treat malformed external input as a normal boundary case: return a clear,
  actionable error and keep the process alive when recovery is possible.
- Treat broken internal invariants as bugs: fail loudly with `expect` or
  `panic!` rather than silently substituting a default and producing bad state.
- Do not add fallback paths, configuration layers, swappable backends, or
  defensive machinery for failures the project has not observed.

### Rust style

- Prefer ordinary, idiomatic Rust: `?`, `Option`, iterators, pattern matching,
  and useful derives.
- Use enums and exhaustive `match` for closed data models. Let the compiler
  expose missing cases instead of erasing the model behind trait objects.
- Prefer owned data in structs. Clone freely when it keeps the design clear;
  avoid viral lifetime parameters and zero-copy work until measurement justifies
  them.
- Use concrete types until multiple real implementations earn an abstraction.
  No speculative traits, generics, builders, registries, or dependency
  injection for a single caller.
- Keep dependencies few. Do not add a crate to abstract something used once.

### Structure and configuration

- Keep one module focused on one concern, with shallow module trees and clear
  boundaries. Cohesion matters more than short files.
- Hardcode local tuning values as nearby `const`s until the project genuinely
  needs user-facing configuration.
- Prefer direct functions and data flow over framework-like plumbing. Split a
  module when the boundary clarifies responsibility, not merely because the
  file is long.

### Errors, tests, and comments

- Use the existing project error conventions for I/O and orchestration. Add a
  custom error type only when callers recover differently based on its variants.
- Do not use quiet fallbacks such as `unwrap_or_default()` when they can hide a
  violated invariant or turn bad input into incorrect state.
- Test behavior at the boundary that matters. Prefer focused unit tests for
  tricky, stable logic and end-to-end tests for observable behavior; avoid
  tests that freeze internals while their design is still changing.
- Every fixed bug should gain a regression test when practical.
- Comments explain why, surprising behavior, or an external rule. Do not write
  comments or doc comments that merely restate the code.

### When to harden

Harden only after the design has proved itself, and against failures the project
has actually observed. Then add the abstractions, typed recovery paths, tighter
lifetimes, configuration, and tests that the stable behavior has earned.

For tools that change files:

- Once a file change starts, let it finish before acting on cancellation.
- After all tool calls in a model response finish, save the assistant message
  and tool results together with the existing `SessionStore::append_batch`.
- Keep the session marked busy until that save attempt and response handling
  finish.
- Add no database tables or restart recovery for tool calls without a separate
  design decision.
