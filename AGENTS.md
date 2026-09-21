THIS DOCUMENT MUST BE KEPT UP TO DATE

# Source Map

- `Makefile`: Fast-by-default and release local installs that replace the Ox
  binary and delete the disposable local session database.
- `scripts/run.py`: Temporary workspace runner for a headless prompt, with
  optional checkout of a pinned GitHub commit.
- `src/main.rs`: Command parsing and process entry; starts the ACP server, runs
  one headless prompt, or runs a credential command.
- `src/auth.rs`: Environment and operating-system keyring credential storage.
- `src/openrouter.rs`: OpenRouter model catalog and effort mapping, request
  encoding, client, and streamed-response assembly.
- `src/tools.rs`: Concrete tool schemas, tool call titles, and execution of one
  complete call.
- `src/tools/read.rs`: Bounded text-file reading with line pagination.
- `src/tools/shell.rs`: Noninteractive shell execution, bounded output tails,
  and process-group cleanup on exit, timeout, or cancellation.
- `src/tools/search.rs`: Bounded glob and grep searches through ripgrep.
- `src/tools/patch.rs`: Patch parsing, exact text matching, workspace path
  validation, and filesystem changes.
- `src/tools/patch-guide.txt`: The patch format guidance shipped as the
  `apply_patch` tool description.
- `src/sessions.rs`: Transcript and session-setting types with their stored
  JSON encoding, transcript validation, and `SessionStore` over one SQLite
  connection.
- `src/acp.rs`: Connection wiring, `ServerState`, lazy OpenRouter client,
  request handlers, per-session configuration selections, and the headless
  prompt entry point.
- `src/acp/operations.rs`: One active prompt, load, or delete per session,
  enforced by an operation guard.
- `src/acp/prompt.rs`: One prompt run: save the user message, request model
  output, request shell approval over ACP, run tools, save complete assistant
  batches, and respond. Headless runs automatically approve tools.
- `src/acp/convert.rs`: ACP input conversion, session update construction
  including each tool call's kind, and transcript replay.

## Testing

Use the `OPENROUTER_API_KEY` in `.env` when testing to avoid keychain prompts.

Use `ox run [--dir <workspace>] '<prompt>'` for live end-to-end testing when a
change affects model requests, tool execution, or transcript persistence. It
creates a new session in `ox.db`, writes no successful output, and reports
completion through its exit status. Set `OX_DATA_DIR` to a temporary directory
when the test should not modify the normal database, then inspect that database
to verify the saved session.

## Backwards Compatibility

Currently, there is none. Delete and recreate `~/.local/share/ox/ox.db` rather
than introducing migrations, versions, etc.

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
- Say session title or tool call title. Never write an unqualified title, which
  could mean either.
- Keep external names such as `cwd`, `reasoning_details`, and
  `AgentThoughtChunk` at their protocol boundaries.

### Glossary

| Term                        | Definition                                                                                                                                                                                                      |
| --------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| ACP                         | Agent Client Protocol, the JSON-RPC interface between an editor or other client and Ox.                                                                                                                         |
| ACP client                  | The editor or application connected to Ox. It is distinct from the OpenRouter and HTTP clients.                                                                                                                 |
| Agent                       | Ox as presented through ACP.                                                                                                                                                                                    |
| ACP update                  | A `session/update` notification that describes session metadata, model output, or tool state. Sending one does not confirm that the ACP client received or displayed it.                                        |
| ACP tool status             | The client-facing state of a tool call: pending, in progress, completed, or failed. A cancelled Ox tool outcome is presented as failed because ACP has no separate cancelled tool status.                       |
| Session                     | A saved conversation and its metadata, identified by a session ID. Its OpenRouter model is fixed when the first turn starts.                                                                                   |
| Session settings            | The model and effort level in force for a turn.                                                                                                                                                                 |
| Effort level                | One of Ox's four reasoning levels: Default, Low, Medium, or High.                                                                                                                                                |
| Effort mapping              | The per-model table that turns an effort level into an OpenRouter effort string, or into no reasoning parameter for Default.                                                                                    |
| Session title               | The short label a session shows in a client, taken once from the first nonblank line of the first saved user message and shortened to 80 characters.                                                            |
| Session summary             | A session's ID, workspace path, optional session title, and creation and activity timestamps, without its transcript.                                                                                           |
| Stored session              | A session summary paired with its validated transcript.                                                                                                                                                         |
| Session store               | The SQLite-backed component that creates, reads, lists, updates, and deletes sessions and their transcripts.                                                                                                    |
| Workspace path              | The exact absolute path associated with a session. Ox does not normalize or resolve aliases.                                                                                                                    |
| Session operation           | One prompt, load, or delete running for a session. At most one can run for the same session at a time.                                                                                                          |
| Operation guard             | A value that keeps one session busy for a session operation. Dropping it makes the session available.                                                                                                           |
| Prompt request              | One ACP request containing user content for a session.                                                                                                                                                          |
| Prompt run                  | The work caused by one prompt request: save the user message, request model output, run tools, save results, and respond.                                                                                       |
| Prompt outcome              | The internal reason a prompt run stopped. It determines how unfinished tool calls are completed and whether Ox returns an ACP stop reason or an error.                                                          |
| Prompt cancellation         | A per-prompt signal that remains cancelled once triggered. It stops new work but does not roll back model or tool effects already observed.                                                                     |
| User message                | Text produced from the supported ACP content blocks and saved before the first model request.                                                                                                                   |
| Transcript                  | The ordered, saved conversation used for both session replay and future model requests.                                                                                                                         |
| Transcript entry            | A model entry, effort entry, user message, assistant message, or tool result in the transcript.                                                                                                                 |
| Model entry                 | The first transcript entry. It stores the OpenRouter model used for every model request in that session.                                                                                                        |
| OpenRouter client           | The concrete client that verifies the API key and sends model requests to OpenRouter's chat-completions endpoint.                                                                                               |
| Model request               | One OpenRouter chat-completion HTTP request. A prompt run may make several.                                                                                                                                     |
| Completion stream           | The reader for one streamed OpenRouter response after its HTTP request has succeeded. It yields output deltas followed by one completion.                                                                       |
| OpenRouter stream item      | An answer-text delta, a reasoning delta, or the one completion yielded by a completion stream.                                                                                                                  |
| Assistant message           | The validated model output assembled from a completion stream: answer text, visible reasoning, tool calls, and continuation metadata.                                                                           |
| Completion                  | An assistant message paired with the normalized reason OpenRouter stopped generating.                                                                                                                           |
| OpenRouter stop             | The normalized OpenRouter stopping reason attached to a completion: finished, tool calls, token limit, or refusal.                                                                                              |
| Answer text                 | The model's user-facing answer. Deltas may be sent live; the assembled text is saved only as part of a complete assistant batch.                                                                                |
| Visible reasoning           | Reasoning text presented to the ACP client. It is distinct from opaque continuation metadata.                                                                                                                   |
| Continuation metadata       | Opaque data stored internally as `continuation_metadata` and encoded as OpenRouter `reasoning_details` on a later model request. It is not displayed as reasoning.                                              |
| Tool call                   | A model-produced call ID, tool name, and raw argument string.                                                                                                                                                   |
| Tool call title             | The one-line description an ACP client shows for a tool call, built from the call's arguments and shortened to 80 characters. A call whose arguments are missing or malformed is titled by its tool name alone. |
| Tool kind                   | The ACP category that tells a client which icon to show for a tool call: execute, read, search, edit, or other.                                                                                                 |
| Tool outcome                | What Ox knows happened: completed, failed, or cancelled, with explanatory text.                                                                                                                                 |
| Tool result                 | A tool call's ID and name paired with its outcome.                                                                                                                                                              |
| Assistant batch             | One assistant message plus exactly one final tool result for each call in the message. The store saves it in one transaction.                                                                                   |
| Uncommitted assistant batch | A validated assistant message whose tool outcomes are incomplete or have not yet been saved.                                                                                                                    |
| Shell permission request    | An ACP request asking whether one shell tool call may run. Approval or denial applies only to that call.                                                                                                        |
| Replay                      | Sending saved transcript content back to the ACP client when a session is loaded.                                                                                                                               |
| Commit                      | A successful SQLite transaction. `PromptRun::commit` saves one complete assistant batch, updates session activity, and only then extends the in-memory transcript.                                              |
| Provisional output          | Answer text or visible reasoning sent to the ACP client before the assistant message is validated. It is not saved if the model request is interrupted.                                                         |

## Just Enough Rust

> Make things as simple as possible, but not simpler.

This is an experiment. Optimize for code that is cheap to change, not robust to
operate. Keep the domain behavior correct; keep the Rust implementing it thin,
boring, and easy to replace. Minimize committed surface area. When in doubt, do
less.

Default to the simplest thing that compiles and reveals whether the idea works.
Leaving a `// TODO:` or a `todo!()` for an unneeded case is better than building
speculative hardening around a design that is still moving.

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
- Use concrete types until multiple real implementations earn an abstraction. No
  speculative traits, generics, builders, registries, or dependency injection
  for a single caller.
- Keep dependencies few. Do not add a crate to abstract something used once.

### Structure and configuration

- Keep one module focused on one concern, with shallow module trees and clear
  boundaries. Cohesion matters more than short files.
- Hardcode local tuning values as nearby `const`s until the project genuinely
  needs user-facing configuration.
- Prefer direct functions and data flow over framework-like plumbing. Split a
  module when the boundary clarifies responsibility, not merely because the file
  is long.

### Errors, tests, and comments

- Use the existing project error conventions for I/O and orchestration. Add a
  custom error type only when callers recover differently based on its variants.
- Do not use quiet fallbacks such as `unwrap_or_default()` when they can hide a
  violated invariant or turn bad input into incorrect state.
- Test behavior at the boundary that matters. Prefer focused unit tests for
  tricky, stable logic and end-to-end tests for observable behavior; avoid tests
  that freeze internals while their design is still changing.
- Every fixed bug should gain a regression test when practical.
- Comments explain why, surprising behavior, or an external rule. Do not write
  comments or doc comments that merely restate the code.

### When to harden

Harden only after the design has proved itself, and against failures the project
has actually observed. Then add the abstractions, typed recovery paths, tighter
lifetimes, configuration, and tests that the stable behavior has earned.

For tools that change files:

- Once `apply_patch` starts changing files, let it finish before acting on
  cancellation.
- Shell cancellation terminates the process group, reaps the shell, and finishes
  bounded output draining before returning; partial changes may remain.
- After all tool calls in a model response finish, save the assistant message
  and tool results together with the existing `SessionStore::append_batch`.
- Keep the session marked busy until that save attempt and response handling
  finish.
- Add no database tables or restart recovery for tool calls without a separate
  design decision.
