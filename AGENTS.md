THIS DOCUMENT MUST BE KEPT UP TO DATE

# Source Map

## Code

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
- `src/sessions.rs`: Transcript and session-setting types with their stored JSON
  encoding, transcript validation, and `SessionStore` over one SQLite
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

## Documentation

Write plans and reviews to (`YYYY-MM-DD-NNN-slug.md`):

- `eng/plans/`
- `eng/reviews/`

Read before planning and changing code:

- `eng/architecture.md`
- `eng/code-style.md`
- `eng/glossary.md`
- `eng/testing.md`

## Backwards Compatibility

Currently, there is none. Delete and recreate `~/.local/share/ox/ox.db` rather
than introducing migrations, versions, etc.

## Comments and Documentation and Communication

- Always describe things directly, clearly, and plainly
- Follow big idea up front and progressive disclosure
- Never use jargon, invented terms, or shorthand
- Never not mix definitions or overload terms

## Testing

See [eng/testing.md](eng/testing.md). Keep the suite flat or smaller.

## Naming

Use the exact terms in [eng/glossary.md](eng/glossary.md).

## Just Enough Rust

Follow [eng/code-style.md](eng/code-style.md).
