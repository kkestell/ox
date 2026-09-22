THIS DOCUMENT MUST BE KEPT UP TO DATE

## Code

- `Makefile`: Release builds and fast-by-default or release local installs.
  Installs replace the Ox binary and delete the disposable session database.
- `scripts/run.py`: Temporary workspace runner for a headless prompt, with
  optional model, effort, and checkout of a pinned GitHub commit.
- `src/main.rs`: Command parsing and process entry; starts the ACP server, runs
  one headless prompt with an optional model and effort, runs a credential
  command, or prints help.
- `src/auth.rs`: Environment and operating-system keyring credential storage.
- `src/system_prompt.rs`: Assembly of Ox's built-in system prompt with bounded
  workspace-root `AGENTS.md` instructions.
- `src/prompts/system_prompt.md`: The editable built-in instructions that define
  Ox's coding-agent behavior.
- `src/prompts/init_prompt.md`: The editable user prompt dispatched by `/init`,
  including the `AGENTS.md` template.
- `src/openrouter.rs`: OpenRouter model catalog and effort mapping, request
  encoding, context limits, client, streamed response assembly, and explicit
  input-context overflow errors.
- `src/compaction.rs`: Request estimates, safe transcript cuts, bounded
  summarizer input, checkpoint commits, and model-request projection.
- `src/prompts/compaction_prompt.md`: Dedicated summarizer instructions.
- `src/tools.rs`: Concrete tool schemas, tool call titles, and execution of one
  complete call.
- `src/tools/read.rs`: Bounded text-file reading with line pagination.
- `src/tools/shell.rs`: Noninteractive shell execution, bounded output tails,
  and process-group cleanup on exit, timeout, or cancellation.
- `src/tools/search.rs`: Ripgrep file discovery, workspace-checked candidates,
  and bounded glob and grep results.
- `src/tools/patch.rs`: Patch parsing, exact text matching, workspace path
  validation, and filesystem changes.
- `src/tools/workspace.rs`: Descriptor-relative file operations shared by
  read, search, and patch tools.
- `src/sessions.rs`: Transcript and durable model, effort, and mode setting
  types, compaction checkpoints, stored JSON encoding, transcript validation,
  database path selection, and `SessionStore` over one SQLite connection.
- `src/acp.rs`: Connection wiring, `ServerState`, lazy OpenRouter client,
  request handlers, advertised slash commands and their prompt dispatch,
  per-session model, effort, and mode selections, system prompts assembled when
  a session becomes active, guarded `/compact`, and the automatic headless
  prompt entry point.
- `src/acp/operations.rs`: One active prompt, load, or delete per session,
  enforced by an operation guard.
- `src/acp/prompt.rs`: One prompt run: reject oversized input before saving,
  save accepted input with captured settings, compact before large model
  requests, retry explicit input overflow once, run tools, save complete
  assistant batches, and respond.
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

Currently, there is none. Recreate `ox.db`, `ox.db-shm`, and `ox.db-wal` instead
of adding migrations or versions. Their directory is `$OX_DATA_DIR`, else
`$XDG_DATA_HOME/ox`, else `~/.local/share/ox`; local install targets do this.

## Communication

- Always describe things directly, clearly, and plainly
- Follow big idea up front and progressive disclosure
- Never use jargon, invented terms, or shorthand
- Never mix definitions or overload terms
