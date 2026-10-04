Keep this document accurate and short.

## Code style

- Do less. Keep correct code simple, ordinary, and cheap to change.
- Write idiomatic Go. Prefer plain data and direct functions.
- Give each concern a clear boundary and each lifecycle one owner.
- Let real needs earn abstractions, dependencies, and configuration. Measure
  before optimizing; harden against observed failures.
- Return clear errors for bad input; fail loudly on broken invariants. Test
  observable behavior; write comments that explain why.

## Code

- `server/` — the Go module. `cmd/ox-server` is the Ox server: ACP, turns,
  tools, OpenRouter, and session store. `cmd/ox` is the terminal client and its
  terminal tests. `server/internal/` holds one package per concern.
- `server/internal/sysprompt/` — the built-in system prompt.
- `examples/` — the example settings file.
- `scripts/run.py` — runs one headless prompt in a temporary workspace.
- `scripts/export_session.py` — exports a session to JSON.
- `.github/workflows/` — the release build.
- `research/` — research notes and reports.
- `agents/` — plans, reviews, and work logs.

## Validation

- `make check` runs every check. Run it after changing code.
- `make check-docs` checks the Markdown. Run it after changing only docs or
  comments.
- `make e2e` runs the isolated tmux tests. Run it after changing terminal
  behavior.
- `make format` formats the code and the Markdown.

Report any check that fails or is skipped.

## Documentation

Use only `README.md`, `AGENTS.md`, the code and its comments, plans, reviews,
work logs, and Git history. Never create new documentation files.

`README.md` tells users how to install, configure, and use Ox. Update it only
when those instructions change. `AGENTS.md` gives instructions to agents and
should rarely change.

The code describes the implementation. Comments explain non-obvious reasons or
external rules; they never restate code. Plans, reviews, and work logs record
work in `agents/`. Git history records changes.

A fact lives in one place. Do not duplicate it across these sources. Most
changes need no documentation or comment edits. Correct or delete inaccurate
text without expanding it.

## Backwards Compatibility

Currently, there is none. Recreate `ox.db`, `ox.db-shm`, and `ox.db-wal` instead
of adding migrations or versions. They live in `$OX_DATA_DIR`, else
`$XDG_DATA_HOME/ox`, else `~/.local/share/ox`.

## Communication

- Always describe things directly, clearly, and plainly
- Follow big idea up front and progressive disclosure
- Never use jargon, invented terms, or shorthand
- Never mix definitions or overload terms
