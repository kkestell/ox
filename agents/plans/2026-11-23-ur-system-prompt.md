# Give Ur a system prompt

## Goal

Resolves [#47](https://github.com/kkestell/ox/issues/47). Ur sends only the
conversation, so its model is not told that it is a coding agent working
through its shell tool, the session's workspace directory, the OS, or the date,
and it never sees the workspace's `AGENTS.md`. Every Ur request starts with a
system message that states these and carries the workspace's `AGENTS.md`.

In the same change, the built-in DeepSeek V4.1 Flash model pins only
`inference-net/us`. OpenRouter no longer lists `stepfun/fp8` for
`deepseek/deepseek-v4.1-flash-1017`, and requests are sent with
`allow_fallbacks: false`, so the second entry is dead.

## Related code

- `internal/ur/ur.go` — `request()` takes queued inputs and posts `body()`;
  `body()` builds the request from `conversation(a.history)` and the settings.
  `a.workspace` is set at start from `agent.Options.Workspace`, the directory
  the shell tool starts in.
- `internal/ur/stream.go` — `message`, the request's message shape, which a
  `system` message uses unchanged.
- `internal/claude/claude.go` — `systemPrompt()`, whose first three sentences
  are Ox's style, which Ur's prompt shares.
- `internal/ur/ur_test.go` — `start`/`run` drive the agent against an
  `httptest` server that records each request body; `field` reads every
  message's field, which `roles` and `contents` use.
- `internal/config/default.json` — the built-in catalog, including the Ur
  models' `providers`.
- `internal/server/live_test.go` — `TestLiveUr` runs the built-in
  configuration against OpenRouter with DeepSeek V4.1 Flash.

## Decisions

- **Built per request, not stored.** `body()` builds the system message each
  time, reading `<workspace>/AGENTS.md` then. The message is derived from the
  workspace, the clock, and the file, so it is never saved to the conversation
  file and has no copy to keep in sync. Edits to `AGENTS.md` and a change of
  date reach the next request. The text is otherwise fixed, so a provider's
  prefix cache holds across requests until one of those changes.
- **Content.** In order:
  1. Role: "You are Ur, a coding agent running inside Ox. The user talks to
     you through Ox. You work in the user's workspace through the `shell`
     tool: read, search, edit, build, and test with shell commands, and check
     your work by running it."
  2. Ox's style, the same three sentences Claude gets.
  3. Environment lines: `Workspace: <path>`, `OS: <GOOS>/<GOARCH>`,
     `Shell: bash`, `Date: <local date, YYYY-MM-DD, weekday>`.
  4. "A directory may hold an `AGENTS.md` with instructions for the files
     beneath it; read it before changing files there."
  5. When `<workspace>/AGENTS.md` exists: "Instructions from the workspace's
     `AGENTS.md`:" followed by its contents verbatim.
- **Ox's style has one source.** Move the three style sentences out of
  `claude.systemPrompt()` into an exported constant `agent.Style` in
  `internal/agent/agent.go`; both harnesses read it. Claude's appended prompt
  is byte-identical after the move.
- **Unreadable `AGENTS.md`.** A missing file (`fs.ErrNotExist`) omits item 5.
  Any other read error ends the turn with the error result
  `Ur could not read AGENTS.md: <err>`, the same way a failed save ends it; the
  prompts already taken into the turn stay saved, as after a failed request.
  No size limit: an oversized file reaches OpenRouter, whose refusal reaches
  the turn's result like any other.
- **Shape.** `body()` becomes `body() ([]byte, error)`, prepending
  `message{Role: "system", Content: …}` to `conversation(a.history)`; a helper
  `instructions(workspace string, now time.Time) (string, error)` in a new
  `internal/ur/instructions.go` builds the text. `request()` finishes the turn
  on its error instead of starting `stream`.
- **Requirements:** **Ur sessions** holds: the conversation file is unchanged,
  so parking and resuming are unaffected, and a resumed conversation gets a
  freshly built system message. **Ur's shell commands** is unchanged. **Built-in
  catalog** is unchanged; it does not list providers. Adds **Ur's
  instructions** (below).
- **Settings:** `providers` for DeepSeek V4.1 Flash becomes
  `["inference-net/us"]` in `default.json`. Only new configuration files get
  it; an existing file keeps its own list, which the user has already edited.
- **Settling:** the `AGENTS.md` read is a local file read on the session
  goroutine, like `save`; it adds no wait.
- **Construction:** the system message exists only in the request body, built
  from its sources at send time, so no stored copy can go stale. Ox's style
  lives only in `agent.Style`.
- **Trust boundaries:** Ur reads one more file, `<workspace>/AGENTS.md`, inside
  the workspace the user opened, and sends it to OpenRouter as the
  conversation already is. Its shell tool can already read the same file.

## Check your assumptions

1. **Ur reads the workspace's `AGENTS.md` itself.**
   - **Plan:** Every Ur request carries the workspace's `AGENTS.md`, read when
     the request is sent, so Ur follows the project's instructions from its
     first reply and picks up edits to them at once. Each request costs the
     file's tokens.
   - **Alternative:** Ur's instructions only tell it to read `AGENTS.md`. Ur
     spends a shell command on it at the start of a conversation, which in
     manual mode the user must approve, and it may skip it.
2. **Ur speaks in Ox's style.**
   - **Plan:** Ur gets the same style instructions as Claude sessions (lead
     with the main point, crisp and deadpan), so both harnesses answer in one
     voice.
   - **Alternative:** Ur answers in each model's own default voice.
3. **New requirement "Ur's instructions."**
   - **Plan:** adds the requirement under Requirement changes.
   - **Alternative:** no requirement; the behavior can then change without
     anyone calling it a bug.

## Test plan

- System message present and fresh per request — Ur harness, new
  `TestInstructions` in `internal/ur/ur_test.go`, since no existing test
  inspects the message list's head. With `AGENTS.md` in `r.workspace` holding
  a marker: the first request's first message has role `system` and contains
  `r.workspace`, `agent.Style`, and the marker. After rewriting `AGENTS.md`
  with a second marker, the next request's system message carries the second
  marker and not the first. Without `AGENTS.md`: a system message still leads
  and contains `r.workspace`, with no `AGENTS.md` contents heading.
- Unreadable `AGENTS.md` — same test, subtest: `AGENTS.md` is a directory; the
  turn's result is an error starting `Ur could not read AGENTS.md: `, and the
  server receives no request.
- Not saved to the conversation — `TestResume`: the resumed request has exactly
  one `system` message, first.
- Existing role and content assertions — `field` skips `system` messages, so
  `roles` and `contents` keep describing the conversation.
- Claude's prompt unchanged — `internal/claude` tests that cover
  `systemPrompt()` keep passing with the style read from `agent.Style`.
- Pinned provider accepts the system message and the single-provider order —
  `TestLiveUr`, unchanged, through `make live`.

## Implementation plan

1. `internal/config/default.json`: DeepSeek V4.1 Flash `providers` becomes
   `["inference-net/us"]`.
2. `internal/agent/agent.go`: add `Style`, the three style sentences.
   `internal/claude/claude.go`: `systemPrompt()` starts with
   `agent.Style + "\n\n"`.
3. `internal/ur/instructions.go` (new): `instructions(workspace, now)` builds
   the text listed under Decisions.
4. `internal/ur/ur.go`: `body()` returns `([]byte, error)` and prepends the
   system message; `request()` finishes the turn with
   `Ur could not read AGENTS.md: <err>` on error. Update the package comment to
   say each request carries Ur's instructions before the conversation.
5. `internal/ur/ur_test.go`: `field` skips `system` messages; add
   `TestInstructions`; extend `TestResume`.
6. Docs and requirement below.

## Requirement changes

- **Added** under `## Ur`, after **Ur's shell commands**:

  **Ur's instructions.** Ur works with instructions stating that it is a coding
  agent in Ox working through its shell tool, Ox's style, the session's
  workspace directory, the OS, and today's date, and with the workspace's
  `AGENTS.md` as it stands when each request is sent.

## Documentation updates

- `docs/architecture.md`: the `ur` row adds that each request starts with a
  system message built from the workspace, the OS, the date, and the
  workspace's `AGENTS.md`, which is never saved to the conversation file.
