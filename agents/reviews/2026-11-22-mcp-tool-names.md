# Review: Ox's MCP tools by readable names and useful summaries

## Scope and coverage

Uncommitted changes on `mcp-tool-names` against base `b5419ba` (HEAD and the
merge base with `origin/main`), checked against
`agents/plans/2026-11-22-mcp-tool-names.md`, which resolves #14. The working
tree held the six modified files and the new `cmd/ox/web/toolInput.test.mjs`
and plan. The plan's one assumption was settled as it recommends: a
`CreateSession` call shows the child's title, and a `ReadSession` call shows
the session ID.

The change resolves #14: Ox's tools show as `ListModels`, `ListSessions`,
`SendMessage`, `ReadSession`, and `CreateSession` in the transcript and on the
approval card; `SendMessage` shows its message, `CreateSession` its title, and
an empty input no `{}`. `docs/functional-requirements.md` is unchanged, as the
plan's Requirement changes say. **Tool calls** and **Permission requests**
hold with the label as the name. The prefix matches the server name and
approved-tool prefix in `internal/claude/claude.go`.

Gaps:

- No `/ox-work` final response was passed on, so the Tests proved and Checks
  results were not available. The checks below were run by the reviewer.

## Summary

| ID | Severity | Category | Action | Status   | Title                                                 |
| -- | -------- | -------- | ------ | -------- | ----------------------------------------------------- |
| F1 | Low      | Testing  | Fix    | Resolved | No test covers the approval card's empty-input `Input` |

## F1. No test covers the approval card's empty-input `Input`

Low · Testing · Fix · Resolved · `cmd/ox/web/src/ApprovalRequest.tsx:34`

The plan's Approval card decision says an empty summary shows `Input`. The
card now computes `(plan() ? "" : toolSubject(...)) || "Input"`, so a call with
`{}`, such as a `ListSessions` approval, shows `Input`. No kept test observes
that case: the only assertion on `Input` is the live ExitPlanMode test in
`approval.spec.ts`, which covers the plan branch only. A regression would
leave an empty-input card with a blank summary row.

### Evidence

Replacing the expression with the pre-change form,
`plan() ? "Input" : toolSubject(...)`, which drops the empty-input fallback,
left `e2e/session.spec.ts` and `e2e/approval.spec.ts` passing (78 passed,
4 skipped). The edit was reverted.

### Action

Keep the code. Add an e2e assertion that an approval for an empty input shows
`Input` as its summary, and that its tool call's summary shows no `{}`.
Carry it in an existing spec that already calls `ask(page, "Bash", "{}")`,
such as "a prompt sent while Claude works stays pending until Claude takes
it", or with `ask(page, "mcp__ox__list_sessions", "{}")` in the new "Ox's own
tools show readable names and the message they send" test.

## Checks run

- `make test`: passed, including the three new `toolInput.test.mjs` cases.
- `make e2e`: 118 passed, 4 skipped.
- Spot check: `aria-label={props.tool.name}` instead of `toolLabel(...)` in
  `ToolCall` fails the new e2e test. Reverted.
- Spot check: dropping the empty-input fallback on the approval card fails no
  test (F1). Reverted.
- By reading: removing the empty-input rule or the Ox subject table in
  `toolSubject` fails the new unit tests (`Bash` `{}` would give `"{}"`;
  `send_message` would give `abc`).
- `make live` is not required: the change does not alter how Ox drives Claude
  Code. A headless Chrome check through `make dev` is not required: the change
  does not alter how the page reaches `oxd`.

## Verification 1

Checked the uncommitted tree on base `b5419ba` after the fix. The only change
since the review is in `cmd/ox/web/e2e/session.spec.ts`: the test "Ox's own
tools show readable names and the message they send" now allows the
`SendMessage` approval, then asks for `mcp__ox__list_sessions` with `{}`. It
asserts the card `Allow ListSessions?` with summary `Input`, and checks that
the tool call's summary has no `{}`. The change brings no regressions and no
new findings.

| ID | Status   | Evidence                                                                                                                                                       |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | With the card's expression changed to `plan() ? "Input" : toolSubject(...)`, the test fails (`Expected: "Input"`, `Received: ""`). It passes with the fix restored. |

Checks run: `make test` passed (19 node tests, Go packages ok). `make e2e` ran
118 tests: 118 passed, 4 skipped. The fault-injection edit was reverted.

## Verdict

Ready to commit. F1 is resolved.
