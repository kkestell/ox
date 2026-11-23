# Show Ox's MCP tools by readable names and useful summaries

## Goal

Resolves [#14](https://github.com/kkestell/ox/issues/14). A call to one of Ox's
own MCP tools shows Claude's full tool name, such as `mcp__ox__send_message`,
in the transcript and on its approval card, and its summary is the input's
first string, so `send_message` shows the recipient's session ID. When done,
Ox's tools show as `ListModels`, `ListSessions`, `SendMessage`, `ReadSession`,
and `CreateSession`, the way built-in tools show; `SendMessage` is summarized
by its message, `CreateSession` by the child's title, and a call with an empty
input, such as `ListSessions`, shows no summary instead of `{}`.

## Related code

- `cmd/ox/web/src/toolInput.ts` — `toolSubject` picks a call's summary as the
  first string in its input, or compact JSON when it has none. Home of the
  readers of Claude's tool calls, already keyed by Claude's tool names
  (`planOf`, `questionsOf`).
- `cmd/ox/web/src/SessionView.tsx:164` — `ToolCall` renders `tool.name` and
  `toolSubject(input)` in the transcript, and uses the name as the `<details>`
  element's `aria-label`.
- `cmd/ox/web/src/ApprovalRequest.tsx:65` — `PermissionCard` titles the card
  `Allow {tool_name}?` and summarizes the input with `toolSubject`.
- `internal/claude/claude.go:140` — registers Ox's MCP server as `ox`, so
  Claude names its tools `mcp__ox__<tool>`.
- `internal/server/mcp.go:136` — the five tools and their input schemas:
  `send_message` `{to, text}`, `create_session` `{name, title, message, …}`,
  `read_session` `{to, cursor}`, `list_models` and `list_sessions` `{}`.
- `internal/fake/fake.go:186` — a prompt `approve <tool> <input JSON>` makes the
  fake harness emit a `tool_use` and an approval for that tool, which e2e specs
  use through `ask` in `cmd/ox/web/e2e/ui.ts`.

## Decisions

- **Where the name is mapped:** in the web client. `tool_name` is Claude's
  payload and passes through `oxd` untouched; the web client already reads
  Claude's tool names (`ExitPlanMode`, `AskUserQuestion`) in `toolInput.ts`.
  The protocol, store, and history keep `mcp__ox__send_message`, and
  `read_session`'s text transcript, which a model reads, is unchanged.
- **The label rule:** a name beginning `mcp__ox__` shows its remainder in
  PascalCase (`send_message` → `SendMessage`); every other name shows as it
  arrives, other MCP servers' tools included. The rule is generic so a new Ox
  tool needs no table entry. The `mcp__ox__` prefix is one constant in
  `toolInput.ts`, with a comment naming `internal/claude/claude.go` as where
  the server name comes from. Ur calls no tools, so Claude is the only harness
  that produces these names.
- **The summary rule:** `toolSubject` takes the tool's name. An Ox tool listed
  in a small table of input fields shows that field when it is a string:
  `send_message` → `text`, `create_session` → `title`. `read_session` keeps
  the first-string rule, which yields its `to`. An input with no keys shows an
  empty summary, for every tool; otherwise the first-string rule and the JSON
  fallback stand.
- **Approval card:** titled with the label (`Allow SendMessage?`) and
  summarized with the same `toolSubject`. Plan mode asks for approval of
  `send_message` and `create_session` (they are not read-only), so these cards
  occur. An empty summary shows `Input`, as the card already does for a plan.
- **Accessible name:** `ToolCall`'s `aria-label` uses the label, so it matches
  what is shown; e2e `toolCall(page, "SendMessage")` finds it.
- **Requirements:** **Tool calls** ("shows its name, a summary of its input")
  and **Permission requests** hold, with the label as the name. The change is
  display-only on data the page already has, so a reconnect, reload, or second
  page renders the same text from the same `tool_use` update.
- **Construction:** one function, `toolLabel`, decides every displayed tool
  name, and one function, `toolSubject`, every summary; both callers use them.

## Check your assumptions

1. **What a new session's tool call says.** Kept.
   - **Plan:** A child-session call shows the title the child gets in the
     sidebar, such as `CreateSession Review: F2 child start outside lock`,
     and a read of another session shows that session's ID.
   - **Alternative:** Show the child's first message instead of its title
     (longer, cut at 120 characters), or show the read session's label
     instead of its ID, which needs the page to look the session up and falls
     back to the ID for sessions it does not list.

## Test plan

- Label and summary rules, unit, new `cmd/ox/web/toolInput.test.mjs` run by
  `make test`: `toolLabel` maps each of the five `mcp__ox__` names to its
  PascalCase form and leaves `Bash` and `mcp__context7__query-docs` unchanged;
  `toolSubject` gives `send_message`'s `text`, `create_session`'s `title`,
  `read_session`'s `to`, `""` for `{}`, and `Bash`'s `command`. No existing
  unit test covers `toolInput.ts`; `highlight.test.mjs` shows the pattern.
- Transcript and approval card wiring, e2e, new test in
  `cmd/ox/web/e2e/session.spec.ts` beside "Allow sends the exact numbers
  displayed in the tool input": `ask(page, "mcp__ox__send_message",
  '{"to":"some-session","text":"hello there"}')` shows an approval named
  `Allow SendMessage?` whose summary is `hello there`, and
  `toolCall(page, "SendMessage")` shows `SendMessage` and `hello there` and
  not `mcp__ox__` or `some-session` in its summary. No existing test sends an
  `mcp__ox__` tool.
- Existing specs that call `ask(page, "Bash", "{}")` keep passing; any that
  assert `{}` as a summary are updated to the empty summary.

## Implementation plan

1. `cmd/ox/web/src/toolInput.ts`: add the `mcp__ox__` constant, `toolLabel`,
   and the Ox subject table; change `toolSubject(input)` to
   `toolSubject(tool, input)` with the empty-input rule. Update the file's
   comments to describe the new rules.
2. `cmd/ox/web/src/SessionView.tsx`: `ToolCall` shows `toolLabel(name)` in the
   summary and `aria-label`, and passes the name to `toolSubject`.
3. `cmd/ox/web/src/ApprovalRequest.tsx`: the card title uses `toolLabel`; the
   summary passes the name to `toolSubject` and shows `Input` when it is empty.
4. `cmd/ox/web/toolInput.test.mjs`: the unit cases above; add it to the
   `node --test` line in `Makefile`.
5. `cmd/ox/web/e2e/session.spec.ts`: the e2e case above.
6. `docs/architecture.md`: the `toolInput.ts` row also lists a tool's label.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`: the `toolInput.ts` row in the web client's file
  table, which lists what the file reads from Claude's tool calls.
