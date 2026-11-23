# Review: Give Ur a system prompt

## Scope and coverage

Uncommitted changes on branch `ur-system-prompt` against base `58adccc`
(`git merge-base origin/main HEAD`): `AGENTS.md`, `docs/architecture.md`,
`docs/functional-requirements.md`, `internal/agent/agent.go`,
`internal/claude/claude.go`, `internal/config/default.json`,
`internal/ur/ur.go`, `internal/ur/ur_test.go`, and the new
`internal/ur/instructions.go`. Checked against
`agents/plans/2026-11-23-ur-system-prompt.md`, whose choices and requirement
text the user approved as written, and issue #47, which it resolves.

Gaps:

- The Claude half of `make live` was not run. The change to Claude is the
  move of the style sentences to `agent.Style`, and the appended prompt is
  byte-identical (see Checks run), so the change does not alter how Ox drives
  Claude Code and `AGENTS.md` ("Verification") does not require it.

## Summary

No findings.

## Checks run

- Byte-identical Claude prompt: the old `systemPrompt()` began with the three
  style sentences joined by single spaces, ending `neutrality.\n\n`. The new
  one is `agent.Style + "\n\n"`, where `agent.Style` is the same three
  sentences, same spacing, ending `neutrality.`. The rest of the function is
  untouched. The claim holds.
- Requirements diff: `docs/functional-requirements.md` adds **Ur's
  instructions** under `## Ur` after **Ur's shell commands**, word for word as
  the plan's Requirement changes state, and nothing else.
- Content order and text in `instructions()` match the plan's Decisions: role,
  `agent.Style`, `Workspace`/`OS`/`Shell`/`Date` lines, the nested `AGENTS.md`
  sentence, then the workspace's `AGENTS.md` under its heading when present.
  `Shell: bash` matches `shell.go`, which runs `bash -c`.
- `body()` has one caller, `request()`, which finishes the turn with
  `Ur could not read AGENTS.md: <err>` before starting `stream`; prompts taken
  into the turn are already saved, as the plan states. The system message is
  never passed to `save`, so the conversation file is unchanged.
- `go test -race ./internal/ur/ ./internal/claude/ ./internal/agent/`: pass.
- Spot-checked removals, each reverted:
  - Treating a missing `AGENTS.md` as an error: `TestInstructions`,
    `TestResume`, and `TestResumeDuringTurn` fail.
  - Dropping the `Date:` line: `TestInstructions` fails.
  - Sending a request instead of finishing the turn on a read error:
    `TestInstructions` ("unreadable AGENTS.md") fails.
- Implementer's checks, taken from the request: `make test`, `make e2e`,
  `make lint`, and `TestLiveUr` passed on the final tree.

## Verdict

`Ready to commit`.
