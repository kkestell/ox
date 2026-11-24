# Title top-level sessions with a configured model

## Goal

A top-level session is labeled with the whole first line of its first prompt,
so a long prompt becomes a paragraph-long label in the sidebar, the session
header, `list_sessions`, and the sender line above every message the session
sends another.

When the change is done, the first prompt the user sends a top-level session is
also sent, once, to a harness, model, and effort the configuration names, and the
short title it writes becomes the session's label everywhere the label shows,
and survives restarts. Until the title arrives, or when titling fails, the
label is the prompt's first line cut to the length a child's title may have.
Every configuration names the titling harness, model, and effort. Resolves [#56](https://github.com/kkestell/ox/issues/56).

This plan assumes PR #64 (child-reports) and then optimistic-ui have landed; it
rebases onto both. Overlap:

- #64 changes `session.update`'s label case to skip prompts with `reports`, and
  `store.UserPrompts` to skip them. Titling hangs off `labelFrom`, so it keeps
  #64's rule: only a prompt with neither `from` nor `reports` is the user's, and
  only the user's first prompt starts titling. This plan does not touch that
  case or `UserPrompts`.
- optimistic-ui adds `session.state` beside `firstLine` and broadcasts it under
  `s.mu` the way `labelFrom` broadcasts the label, and carries `state` beside
  `label` in `workspace/list` and `session/created`. This plan adds one field
  beside them, changes `labelFrom`'s body, and leaves the page untouched.

## Related code

- `internal/server/session.go` — `firstLine`, `labelLocked`, `labelFrom`, and
  `session.update`, which calls `labelFrom` for the user's prompts on the
  session's goroutine. `post` is how other goroutines reach the session.
- `internal/server/server.go` — `New` rebuilds `firstLine` from
  `store.UserPrompts` and builds sessions from `store.Session` records.
- `internal/server/mcp.go` — `titleRunes` (48), the length limit of a child's
  title; `list_sessions` and `messageInput` read `labelLocked`.
- `internal/store/store.go` — schema, `Workspaces` (loads session records with
  `LEFT JOIN`s on `children` and `archived`).
- `internal/agent/agent.go` — `Harness`, the per-harness functions `oxd` calls.
- `internal/claude/claude.go` — `startProcess`'s environment rule for
  subscription billing; `children.Start`. `internal/claude/messages.go` —
  `message`, `failed`, `errorText` for a `result` line.
- `internal/ur/stream.go` — `request`, `Agent.post`, `read`.
  `internal/ur/ur.go` — `Harness`, `Agent.body`.
- `internal/config/config.go`, `default.json` — configuration and its checks.
- `cmd/oxd/main.go` — the harness map `oxd` serves, including `fake`;
  `checkConfig` refuses configured efforts a harness does not take.
- `cmd/ox/web/e2e/services.ts` — the configurations e2e specs start `oxd` with.
- `cmd/ox/web/src/ox.tsx` — already applies `session/labeled` to `label`, which
  the sidebar, header, and menus read. No page change.

## Decisions

- **One-shot calls are a harness function.** `agent.Harness` gains
  `Ask func(ctx context.Context, model, effort, instructions, text string) (string, error)`:
  one stateless request with `instructions` as the system prompt and `text` as
  the user's message, returning the reply's text. Claude and Ur implement it.
  Titling's instructions and cleanup live in `internal/server`, so the rule for
  what a title is sits in one place, and harnesses know nothing about titles.
- **Configuration.** `config.Config` gains `Titling Titling \`json:"titling"\``,
  with `Titling{Harness, Model, Effort string}` (JSON `harness`, `model`,
  `effort`). It is required: `config.Load` refuses a configuration without it,
  or with an empty `harness`, as `titling is required: name the harness, model,
  and effort that title sessions`, and otherwise checks it like `defaults`: the
  harness is configured and `CheckModel(model, effort)` passes, prefixed
  `titling: `. `default.json` adds `"titling": {"harness": "claude", "model":
  "claude-haiku-5-5", "effort": "low"}`. An existing configuration file without
  it, including a `make dev` worktree's saved one, fails to load until the user
  adds it.
- **The server holds its titler.** `Server` gains `ask`, the titling harness's
  `Ask`, resolved once in `New` from `cfg.Titling.Harness`. `New` refuses a
  configuration whose titling harness has no `Ask`, as `titling harness %q
  cannot title sessions`, so `s.ask` is never nil and no caller checks it. This
  is the one check of it; `cmd/oxd`'s `checkConfig` is unchanged and `run`
  reports `New`'s error as it does today.
- **The fake harness titles by echo.** `fake.Ask` returns `text` unchanged, as
  the fake harness echoes, so a fake session's title equals its first-line
  label and existing label assertions and e2e specs hold. `cmd/oxd` registers
  `"fake": {Start: fake.Start, Ask: fake.Ask}`.
- **When titling runs.** `labelFrom` is the one place that sees the user's
  first nonblank prompt. When it sets `firstLine`, it starts
  `go sess.generateTitle(prompt)` after releasing `s.mu`. Since
  `firstLine` is set once per session and restored from history on restart, a
  session is titled at most once and never after a restart; a session from
  before this change keeps its first-line label.
- **The titling goroutine.** `generateTitle` runs on its own goroutine and touches no
  session state. It cuts the prompt to its first `titleInputRunes` (4000) runes,
  calls `Ask` with `titleInstructions` under a context bounded by `titleLimit`
  (one minute) and cancelled when `sess.done` closes, cleans the reply with
  `cleanTitle`, and posts `setTitle` to the session. A failure, an empty reply,
  or a `post` that reports the session ended is logged as
  `session <id>: title: <err>` and changes nothing.
- **`titleInstructions`**, in `session.go`: `"Write a title of at most 48
  characters for a coding session that begins with the user's message. Reply
  with the title alone, in plain text, without quotes or a trailing period."`
- **Shortening.** `shorten(line)` returns `line` when it has at most
  `titleRunes` runes, else its first `titleRunes-1` runes, right-trimmed, plus
  `…`. `firstLine` returns `shorten` of the line it finds, so `New` and
  `labelFrom` both store the short form. `cleanTitle(reply)` takes `firstLine`
  of the reply, strips one pair of enclosing `"`, `'`, or `` ` ``, trims, and
  returns it, empty meaning failure; it is short by construction.
  `create_session` keeps refusing a title over `titleRunes`.
- **`setTitle`** runs on the session's goroutine: it stores the title with
  `store.SetTitle`, logging a failure and keeping the title in memory, as
  `Database failures` requires; then, under `s.mu`, sets `sess.title` and
  broadcasts `session/labeled` with `labelLocked()`.
- **Label.** `labelLocked` returns the child's title, else `title`, else
  `firstLine`, else `id[:8]`. `list_sessions`, `workspace/list`,
  `session/labeled`, and the `from_label` of a message sent from now on all read
  it, so the title shows wherever the label does. A message recorded before the
  title arrived keeps the label it was sent with, as `protocol.md` already says.
- **Store.** New table
  `titles (session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE, title TEXT NOT NULL)`,
  created by the schema's `CREATE TABLE IF NOT EXISTS`, so existing databases
  gain it without a migration. `store.Session` gains `Title string` (empty when
  untitled), loaded in `Workspaces` with `LEFT JOIN titles`. `SetTitle(sessionID,
  title string) error` inserts the row. Deleting a session deletes its title by
  cascade.
- **Claude's `Ask`** (`internal/claude/ask.go`): runs
  `claude -p --output-format=json --safe-mode --no-session-persistence --tools= --disallowedTools=mcp__* --strict-mcp-config --disable-slash-commands --system-prompt <instructions> --model=<model> [--effort=<effort>]`
  with `exec.CommandContext`, `text` on stdin, `Dir` set to `os.TempDir()`,
  `WaitDelay` of one second, and stdout and stderr captured in buffers. It
  starts through `children.Start` with a kill of the process, and calls
  `children.Done` after `Wait`. stdout parsed as one `message` of type
  `result`: success returns `Result`; `failed()` returns `errorText()`.
  Unparseable stdout returns its trimmed text, else stderr's, else the exit
  error. `startProcess`'s environment rule moves into
  `subscriptionEnv(cmd *exec.Cmd) []string`, which both call.
- **Ur's `Ask`** (`internal/ur/stream.go`): reads `OPENROUTER_API_KEY` at call
  time, erroring as `Start` does when it is unset; posts a `request` with
  `[system instructions, user text]`, no tools, `Stream: true`, the effort as
  `reasoning` when not empty, and the model's provider order; and returns the
  concatenated reply text. `Agent.post` becomes a function
  `post(ctx, endpoint, key, body, onDelta)` that `Agent.stream` and `Ask` share.
  `request.Tools` gains `omitempty`. `ur.Harness` sets `Ask` with the endpoint
  and providers it already takes.
- **Requirements.** _Session labels_ changes (Requirement changes). _Messages in
  the transcript_ and _Claude can message other sessions_ hold: both show the
  label, now the title once it exists. _Subscription billing_ holds through
  `subscriptionEnv`. _Stopping oxd_ holds: `Close` ends each session, which
  closes `done`, cancels the titling context, and kills the Claude process,
  which `children.WaitAll` then reaps. _Ox's own files_ holds: Claude runs in
  the existing temp directory and Ox writes only its database. _Independent
  sessions_ holds: titling runs off the session's goroutine. _Configuration
  checks_ covers `titling`, absent or invalid. _Built-in catalog_ holds: the
  written configuration includes `titling`. No page request is added, so a lost reply, reconnect,
  or second page sees only `session/labeled` and `workspace/list`, both queued
  under `s.mu` in one order.
- **Settings.** The titling process runs with exactly the configured model and
  effort. Claude runs an unknown effort at its default; `checkConfig` already
  refuses any configured effort Claude does not take, and titling's effort must
  be one of the model's configured efforts. An unknown model fails the call.
- **Settling.** The one new wait, `Ask`, ends with its reply, at `titleLimit`, or
  when the session ends. `post` returns once the session takes the operation or
  has ended. No page waits on titling.
- **Process I/O.** `cmd.Wait` with `WaitDelay` returns once Claude exits even if
  something holds its pipes. A failure reason on stdout (`Not logged in`, an
  error `result`) or stderr reaches the log line.
- **Construction.** One label rule, `labelLocked`, reading facts each with one
  writer: `firstLine` and `title` are written only by the session's goroutine
  under `s.mu`; titling always exists, since `Load` requires `titling` and
  `New` requires its `Ask`, so `s.ask` is never nil; a title is short because `cleanTitle` and `shorten` produce it, not because
  readers check.
- **Ownership.**
  - `session.title`: owned by the session's goroutine, written under
    `Server.mu`, read by `labelLocked`. Needed so the label shows the title
    after it arrives. Race: a `workspace/list` and the broadcast are ordered by
    `s.mu`. Cleanup: goes with the session.
  - `titles` row: needed so the title survives restarts. Written by `setTitle`;
    deleted by cascade with its session. A store failure leaves the title in
    memory until restart.
  - The titling goroutine and its context: needed to title without holding up
    the session. Races: the session is destroyed or `oxd` stops while it runs,
    which cancels the call and makes `post` report false, so nothing is stored
    or broadcast; the session is archived meanwhile, which still takes the title.
    Cleanup: the context's cancel and the timer on `titleLimit`.
- **Claude Code.** One-shot calls follow `docs/harnesses/claude-code.md`
  section 12 (One-shot utility calls), with `--output-format json` and the
  prompt on stdin in place of stream-json. Checked live on 2.1.331: stdout is
  one `result` object; an unknown model gives `is_error: true` with its reason
  in `result` and exits 1. Section 12 records both. Subscription billing follows
  section 1 (Environment).
- **Trust boundaries.** The first prompt goes to the titling harness's provider,
  which may differ from the session's when the user configures it so. The
  Claude process gets no tools, no MCP servers, and no project instructions.

## Check your assumptions

1. **A session is titled once, from its first prompt, as soon as it is sent.**
   Kept.
   - **Plan:** The title appears a second or two after the first prompt, before
     Claude replies, and never changes after. This changes "Session labels"
     (Requirement changes).
   - **Alternative:** Title after the first reply, which waits for the whole
     first turn, often minutes; or retitle when the topic drifts, which costs a
     call per turn and changes the label under the user.
2. **No renaming in this change.** Kept.
   - **Plan:** The user cannot rename a session; a poor title stays.
   - **Alternative:** Rename from the session's menu, which adds a dialog and a
     rule for whether a rename stops automatic titles.
3. **The first-line label is shortened too.** Kept, in the approved "Session
   labels" text.
   - **Plan:** Until the title arrives, or when titling fails, a session shows
     its first prompt's first line cut to a child title's length, with an
     ellipsis, in the sidebar, header, and sender line alike.
   - **Alternative:** Keep the whole first line as today, which keeps
     paragraph-long sender lines whenever titling fails.
4. **The configuration must name the titling model.** Changed from titling
   off when unconfigured to required.
   - **Plan:** Every configuration names the titling harness, model, and
     effort; a new one titles with Haiku at low effort. `oxd` refuses to start
     with a configuration that leaves it out, saying so, including an existing
     file the user has not yet edited. This adds "Titling model" (Requirement
     changes).
5. **Existing sessions are not retitled.** Kept.
   - **Plan:** Sessions whose first prompt was sent before this change keep
     their (now shortened) first-line labels.
   - **Alternative:** Title every untitled session when `oxd` starts, which
     sends one titling call per old session at once.

## Test plan

- Server test setup (`internal/server/client_test.go`): every test builds its
  server through a helper, `newServer(st, cfg, parkAfter, harnesses, mcp)`,
  which, when `cfg.Titling.Harness` is empty, sets it to a `titler` harness it
  adds with `Ask: fake.Ask`, then calls `New`. The existing `New(...)` calls in
  `internal/server` tests become `newServer(...)`; tests that set their own
  `Titling` pass through unchanged.
- Server (`internal/server/methods_test.go`): `New` with a titling harness that
  has no `Ask` returns its error. This goes beside the existing `New` failure
  case, or in `TestSessionLabel` when there is none.
- Server (`internal/server/methods_test.go`, `TestSessionLabel`): the test's
  fake harness gains an `Ask` that records its calls and answers from a channel
  the test controls, and `cfg.Titling` names it. Cases:
  - The first prompt `"\u0085\nActual title\nmore"` gives `session/labeled`
    `Actual title`, then `Ask` receives the whole prompt and
    `titleInstructions`, and its reply `"\"Generated title\"\nextra"` gives a
    second `session/labeled` `Generated title`; `workspace/list` lists it.
  - A later prompt calls `Ask` no more; after a restart `workspace/list` gives
    `Generated title` and `Ask` is not called.
  - The image-only prompt calls no `Ask`.
  - A first line of 60 runes labels its session with 47 runes and `…`.
  - An `Ask` error leaves B labeled with its first line, with no second
    `session/labeled`.
  - A session destroyed while `Ask` waits: `Ask`'s context is cancelled, and no
    `session/labeled` follows the destroy.
- Server (`internal/server/mcp_test.go`): in the test that has one session
  `send_message` another, with a recording `Ask`, the recipient's first
  message calls no `Ask`. #64's `TestChildReports` keeps covering reports.
- Store (`internal/store/store_test.go`): `SetTitle` then `Workspaces` returns
  the title; deleting the session deletes it.
- Config (`internal/config/config_test.go`): a configuration without `titling`
  is refused with the required error; `titling` with an unknown harness,
  model, or effort is refused; the built-in configuration sets it. The
  existing JSON fixtures that expect to load, or to fail on a later check, gain
  a valid `titling`.
- e2e (`cmd/ox/web/e2e/services.ts`): `fakeConfig` gains `titling` naming
  `fake`, `echo`, `low`, and `haikuConfig` naming `claude`,
  `claude-haiku-5-5`, `low`, so the specs' `oxd` starts. With the fake's echo
  title, no spec's assertion changes.
- Claude harness (`internal/claude/ask_test.go`, new, since no existing test
  runs a one-shot process; uses `script` from `claude_test.go`): the stand-in
  records its arguments, stdin, and whether `ANTHROPIC_API_KEY` is set, and
  prints a success `result`, which `Ask` returns; an error `result` returns its
  text; `Not logged in` on stdout with exit 1 returns that text; empty stdout
  with a stderr reason returns the reason; a cancelled context ends a stand-in
  that sleeps.
- Ur (`internal/ur/ur_test.go`, new `TestAsk`, beside the other request tests):
  the test server sees the model, the system and user messages, no `tools`,
  the effort, and the provider order, and its streamed reply is returned whole;
  a refused status returns OpenRouter's message; an unset key errors.
- Live (`TestLive`): with the built-in configuration, the first prompt of an
  existing top-level section's session gives a second `session/labeled` whose
  label is one nonblank line of at most 48 runes. `TestLiveUr` sets
  `cfg.Titling` to Ur with the built-in DeepSeek model, since its server has no
  Claude harness, and its first prompt gives a second `session/labeled` with a
  nonblank label.

## Implementation plan

1. `internal/agent/agent.go`: add `Ask` to `Harness`.
2. `internal/config/config.go`, `default.json`: required `Titling`, its check,
   and the default. Config tests and fixtures.
3. `internal/fake/fake.go`: `Ask`. `cmd/oxd/main.go`: register it on `fake`.
4. `internal/claude/ask.go`, `claude.go`, `prompt.go`: `Ask`,
   `subscriptionEnv`, and `Ask` in `claude.Harness`. `ask_test.go`.
5. `internal/ur/stream.go`, `ur.go`: shared `post`, `Ask`, `omitempty` on
   `Tools`, `Ask` in `Harness`. `TestAsk`.
6. `internal/store/store.go`: `titles` table, `Session.Title`, `SetTitle`.
   Store test.
7. `internal/server/server.go`: `Server.ask`, resolved and checked in `New`.
   `internal/server/client_test.go`: `newServer`, and the test call sites moved
   to it.
   `internal/server/session.go`: `title` field, `shorten`, `firstLine` through
   `shorten`, `cleanTitle`, `titleInstructions`, `titleInputRunes`,
   `titleLimit`, `labelLocked`, `labelFrom` starting `generateTitle`, `generateTitle`,
   `setTitle`; `newSession` takes `record.Title`. Server tests.
8. Live cases in `internal/server/live_test.go`; `titling` in
   `cmd/ox/web/e2e/services.ts`.
9. Docs below and the approved requirement text; `make docs`.

## Requirement changes

- **Changed:** **Session labels.**
  - Old: A child session is labeled with the title it was created with. Any
    other session is labeled with the first line of the first prompt the user
    sent it, or a short identifier before it has one.
  - New: A child session is labeled with the title it was created with. Any
    other session is labeled with a short title the configured titling model
    writes from the first prompt the user sent it, which replaces the label
    everywhere it shows once it arrives and lasts through restarts. Until then,
    or if titling fails, the session is labeled with that prompt's first line,
    shortened, or a short identifier before it has one.
- **Added** (Configuration): **Titling model.** The user chooses in the
  configuration the harness, model, and effort that title sessions, from those
  Ox offers.

## Documentation updates

- `docs/protocol.md`: `session/labeled` is sent when a top-level session takes
  the user's first prompt, with its first line shortened, and again when its
  generated title arrives; the `label` descriptions in `workspace/list` and
  `list_sessions` name the generated title.
- `docs/architecture.md`: the `agent` row names `Ask`; the `config` row names
  the titling choice.
- `docs/harnesses/claude-code.md` section 12: `--output-format json` with the
  prompt on stdin prints one `result` object; an unknown model gives an
  `is_error` result and exit 1 (2.1.331).
