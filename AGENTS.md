Keep this document accurate and short.

## Code

- `src/` — the agent: the ACP boundary, the prompt run, tools, OpenRouter, and
  the session store.
- `src/prompts/` — the built-in system, subagent, and compaction prompts.
- `examples/` — the example settings file and skills.
- `scripts/run.py` — runs one headless prompt in a temporary workspace.
- `.github/workflows/` — the release build.
- `research/` — research notes and reports.
- `agents/` — agent docs, plans, and reviews.

## Validation

- `make check` runs every check. Run it after changing code.
- `make check-docs` checks the Markdown. Run it after changing only docs or
  comments.
- `make format` formats the code and the Markdown.

Report any check that fails or is skipped.

## Documentation

The code describes what the code does. Docs never restate it: no descriptions of
files, functions, fields, or behavior, and no summaries of changes. Git history
records the changes.

Most changes need no doc edits. Before editing any doc, check whether the change
alters what that doc covers. If it does not, leave the doc alone, even when the
doc mentions the feature. A fact lives in one place, never in several docs. When
a doc passage is wrong, correct or delete it without expanding it. Add a doc or
a section only when asked.

- `AGENTS.md`: instructions for agents and the top-level directory map.
- `agents/architecture.md`: Ox's components, the boundaries between them, what
  each owns, and the decisions that shape them.
- `agents/testing.md`: how to run the tests, where each kind of test goes, and
  test discipline.
- `agents/glossary.md`: naming rules and one-line definitions of domain terms.

Use `YYYY-MM-DD-NNN-slug.md` filenames for plans in `agents/plans/` and code
reviews in `agents/reviews/`. Plan reviews stay in the conversation. Do not
create review documents for plans. Include this rule explicitly when asking
Claude or another agent to review a plan.

Read before planning and changing code:

- `agents/architecture.md`
- `agents/code-style.md`
- `agents/glossary.md`
- `agents/testing.md`

## Backwards Compatibility

Currently, there is none. Recreate `ox.db`, `ox.db-shm`, and `ox.db-wal` instead
of adding migrations or versions. Their directory is `$OX_DATA_DIR`, else
`$XDG_DATA_HOME/ox`, else `~/.local/share/ox`; local install targets do this.

## Communication

- Always describe things directly, clearly, and plainly
- Follow big idea up front and progressive disclosure
- Never use jargon, invented terms, or shorthand
- Never mix definitions or overload terms
