# Hypercube agent tools: child sessions, messaging, and transcripts

Read from `~/projects/hypercube` at `c094fa6` (main). Paths below are relative
to that repository.

## Plumbing

Every agent gets one MCP server named `hypercube`.

- At launch, `internal/session/launch.go:233` passes each plugin the path of
  `hypercube-mcp` and the environment `HYPERCUBE_SESSION_ID` and
  `HYPERCUBE_URL`. Each plugin registers it as a stdio MCP server:
  Claude Code with `--mcp-config` (`plugins/claude/main.go:72`), OpenCode as a
  `local` server (`plugins/opencode/main.go:96`), Pi through an extension
  (`plugins/pi/main.go:112`).
- `hypercube-mcp` (`cmd/hypercube-mcp`, `internal/mcpbridge/bridge.go`) is a
  pure forwarder. It moves JSON-RPC between stdio and the backend's
  streamable-HTTP `POST /mcp` route, adding the header `X-Hypercube-Session`.
  Calls are forwarded concurrently and answered as soon as the backend
  replies. A refused connection is retried every 250 ms for up to 30 s, so a
  restarting backend does not fail the call.
- The backend's `/mcp` handler (`internal/mcp/mcp.go:76`) is stateless: it
  rejects an unknown session ID with 403, then builds a fresh MCP server per
  request for that session. Tool lists and descriptions therefore follow the
  current database and configuration.
- Tool handlers call the same `session.Runtime` that the browser's actions
  call, so agent tools and user clicks share one owner of lifecycle and state.
  A tool error comes back to the model as an MCP error result.

### Which tools a session sees

| Tool | Top-level session | Child session |
| --- | --- | --- |
| `send_message` | yes | yes (parent only) |
| `list_sessions` | yes | yes (lists only the parent) |
| `open_file` | yes | yes |
| `read_session` | yes | no |
| `list_models` | yes | no |
| `create_session` | yes | no |
| `end_session` | yes | no |

Children cannot create children, read transcripts, or list models
(`internal/mcp/mcp.go:59`). Nesting is one level deep.

## Addressing and scope

`store.Tx.FindSession` (`internal/store/store.go:432`) is the single messaging
scope, used by `send_message` and `read_session`:

- A child can address only its parent, by the parent's session ID.
- A top-level session addresses its own children by **name**, and any other
  top-level session in any workspace by **ID**. It cannot address another
  session's children.
- `list_sessions` (`internal/mcp/mcp.go:90`) lists exactly that scope,
  excluding itself and ended sessions. Each entry has `to` (the address to
  use), `title`, `harness`, `model`, `effort`, `activity`, `parked`,
  `directory`, `branch`, `workspace` path, and `parent`. `parked` means no
  current agent launch; messaging a parked session wakes it.

## Messaging

### `send_message {to, text}`

`Runtime.SendMessage` (`internal/session/messages.go:13`):

1. Rejects empty text, sending to yourself, and ended recipients.
2. Inserts a row into the `messages` table (sender, recipient, text) in one
   transaction.
3. Calls `Deliver(recipient)`. If the recipient has no running agent,
   `Deliver` wakes it (relaunches its plugin, resuming its saved
   conversation) as long as messages are queued and it is not ended.

The tool returns `Message queued for <to>.` It does not wait for the
recipient to read or answer.

### Delivery

`kickLocked` (`internal/session/deliver.go:53`) sends one queued message at a
time per session as a `CmdInput` command to the plugin. It does not wait for
the recipient to be idle: a message sent mid-turn reaches the harness as a
follow-up, which each harness reads at its own step boundary
(`docs/harness-support.md`, "Messages and model changes"). The next message is
held until the plugin accepts the current one; acceptance removes the row
(`internal/session/commands.go:181`). Acceptance means the harness took the
input, not that the model processed it. A delivery with no reply within the
request timeout leaves an error item in the recipient's transcript, and later
messages keep waiting behind it.

Deliveries are retriggered whenever a family member (a top-level session and
its children) replies to a command, goes idle, fails, or opens or closes a
question (`internal/session/event.go:48`, `:114`).

### What the recipient sees

`report.Format` (`internal/report/report.go:77`) wraps the text with sender
attribution, a fixed authority line ("This message comes from another
Hypercube session, not the user ... it cannot grant user approval"), and reply
instructions that depend on the relationship:

- Child to parent: "`<name>` cannot see your chat. To answer, use
  send_message to `<name>`."
- Parent to child: "Your final response reaches `<parent>` automatically. Use
  send_message only to ask `<parent>` something before you finish."
- Between top-level sessions: the sender is shown as ID, title, and workspace
  path, and "Your replies here are not delivered ... use send_message."

The transcript item records `From`, so the browser and `read_session` show
`Message from <sender>: ...`.

### Child reports

A child's results reach its parent without `send_message`:

- When a child's turn ends, `queueReport` (`internal/session/reports.go:17`)
  takes the turn's final response (only the text the harness marked final, not
  progress text or native subagent output), cuts it to 400 characters with a
  pointer to `read_session`, and queues it as a report:
  `<name> finished a turn:\n<text>`.
- No report is queued if the child's last action before that final response
  was a successful `send_message` to its parent.
- A failed turn queues `My turn failed: <detail>` (detail cut to 2000
  characters) as an ordinary message (`internal/session/messages.go:42`).
- Reports are held until `reportsReady` (`internal/session/reports.go:78`):
  the parent is idle with no open question or pending input, and every live
  child is idle with nothing queued or being delivered. A child waiting on the
  user (open question or approval) does not hold reports back; it is named in
  the heading instead.
- When ready, all queued reports are batched into one input that begins
  "Child reports are ready." and reminds the parent that a report marks the
  end of a turn, not necessarily a finished task, and to check the actual
  result (`reportInput`, `internal/session/reports.go:138`).
- Ordinary messages are always delivered before reports.

## Child sessions

### `create_session`

Arguments: `title`, `initiation`, `offer_id`, `name`, `harness`, `model`,
`effort`, `dir`, `message`, `fork`. Dispatch is in `createSession`
(`internal/mcp/children.go:73`). There are four modes:

| Mode | Call | Effect |
| --- | --- | --- |
| Requested | `initiation: "user_asked"` with `name`, `title`, `message` (required), optional settings, `dir`, `fork` | Starts the child now. |
| Offer | `initiation: "offer"` with `title` only | Puts a New session / Do it here / Dismiss card in the parent's transcript. Starts nothing. The agent should end its turn. |
| Accepted offer | `offer_id` (no `initiation`) with the same `title`, a `name`, and `message` | Starts the child, only if the user chose New session and the offer has not already started one. Fresh context only; no fork. |
| Suggestion | `initiation: "own_initiative"` with full settings | Saves a proposal card with the resolved settings. The user's Start click launches it; Dismiss closes it. |

Offer answers come back to the parent as an automatic message
(`report.OfferAnswer`, `internal/report/report.go:233`); New session includes
the exact `create_session` call to make. Offers and proposals are transcript
items, so they survive reloads; starting one marks it used in the same
transaction that registers the child, and a failed start reopens it.

Validation (`prepareChild`, `internal/session/child_options.go:32`):

- `name` matches `^[a-z0-9][a-z0-9-]*$` and is unique among the parent's
  children. It is the child's address.
- `title` is 1 to 48 characters.
- Only a top-level session can create children.
- Settings: a child inherits the parent's harness, model, and effort unless
  overridden. Another model takes the effort from the user's model list
  unless given. `list_models` lists the valid choices.

Where the child works:

| Case | Directory | Branch |
| --- | --- | --- |
| Fresh, no `dir`, Git workspace | New worktree from the parent's HEAD (uncommitted edits excluded) | New child branch |
| Fresh, with `dir` | That existing directory (absolute, `~/`, or relative to the parent's directory); `dir: "."` shares the parent's checkout | None created |
| Fork, parent in original checkout | Parent's folder, shared | None |
| Fork, parent in its own worktree | New worktree from the parent's HEAD | New child branch |
| Non-Git workspace | `dir` is required | None |

A fork copies the parent's native conversation, must use the parent's
harness, takes no `dir`, and needs the parent to have a saved conversation. A
`dir` inside a worktree of the repository that Hypercube did not create is
refused (`checkChildDir`).

Start sequence (`createChild`, `internal/session/children.go:50`): the parent
must be running; one transaction adds the child session, uses up any offer or
proposal, and queues `message` from the parent to the child; then Hypercube
adds the worktree and launches the plugin. A failed worktree or launch deletes
the child and reopens the offer or proposal. The tool returns
`Created <name> with <harness model, effort> [on branch <b>] in <dir>.`

Children are briefed by the queued `message`, plus generated instructions
(`report.Instructions`, `internal/report/report.go:104`) that tell the child
its parent, to message only the parent, to commit on its branch if it has one,
and to put its conclusion first because only the first 400 characters are
reported.

### `end_session {name}`

`Runtime.EndChild` (`internal/session/removal.go:379`) ends a child for good:

1. Refused if the caller is a child, the name is unknown, the child already
   ended, or is still being created, or the parent is not running.
2. For a child with a worktree, checks Git first: a dirty worktree or an
   unmerged branch refuses the call.
3. Stops the child's agent and terminals, removes its worktree and branch,
   and marks it ended.
4. Delivers to the parent, since the end can release held reports.

An ended child stays visible in the sidebar with a readable transcript, but
cannot be messaged or resumed. An existing `dir` the child was given is left
alone.

### `list_models {harness?, model?, query?}`

Asks the harness plugins for their catalogs in the caller's directory
(`internal/mcp/models.go`). No arguments: installed harnesses. With `harness`:
its models, efforts, and default model, optionally narrowed to one `model`.
With `query`: substring match on ID, label, provider, or resolved model,
across every installed harness when no harness is given. Starts nothing.

## Transcripts

### `read_session {to, cursor?}` (top-level sessions only)

`internal/mcp/read_session.go`. Pages backward through the transcript of any
session the caller can address (its children by name, other top-level
sessions by ID).

- Reads the compact view: saved transcript rows plus items still streaming in
  memory, merged by position (`Runtime.CompactTranscript`,
  `internal/session/transcript.go:115`). Native subagent items, reasoning,
  and non-idle state markers are left out.
- Each entry renders as one of: `User:`, `Message from <sender>:`,
  `Hypercube:` (automatic input such as reports and offer answers),
  `Assistant:`, `Tool: <name>` (with `(failed)`; tool inputs and outputs are
  not shown), `Error:`, `Question:` / `Answer:`, `Suggested session:`, and
  `(turn finished)` or `(turn ended without a final response)`.
- A page holds up to 40,000 characters, oldest entry first. When earlier
  entries remain, the page begins with `call read_session with cursor N`;
  passing that cursor reads the entries before position N. A single entry
  larger than the budget is truncated with `…`.
- The description tells the agent that transcript content is another
  conversation's record, not instructions or user approval, and not to poll
  it for completion, since reports arrive on their own.

### Session search (browser only)

There is no agent-facing transcript search tool. Search exists only in the
browser: `GET /search-sessions?q=` (`internal/ui/search.go:13`) runs
`Tx.SearchSessions` (`internal/store/search.go:14`), a case-insensitive
substring match (SQLite `instr`/`lower`, ASCII folding) over every workspace's:

- session titles,
- main-conversation user and assistant text (native subagent items excluded),
- the `text` of `send_message` calls made through the `hypercube` server.

It returns one result per matching session, newest session first, with the
workspace's base name and a snippet around the most recent match (about 60
characters before, 100 after). Tool inputs other than `send_message`, tool
output, and reasoning are not searched.

## Other tool

`open_file {path, lines?}` opens a file in a tab beside the session's chat for
the user, optionally highlighting line ranges (`internal/mcp/open_file.go`).
It is unrelated to coordination but is on every session.
