# Ox

Ox is a web app for running many agent sessions at once. Each session runs
Claude Code or Ur, Ox's own agent for models on OpenRouter. A daemon, `oxd`,
keeps your workspaces and sessions and runs their agents, and `ox` serves the
web UI. Claude sessions can message, read, and start each other, so one Claude
can split a job across child sessions.

## Features

### Workspaces and sessions

- Add any local directory as a workspace. Each workspace lists its sessions
  newest first, with child sessions indented beneath their parent.
- Each session shows its status: parked, idle, idle with background work
  running, running, waiting on you, or archived.
- Collapse a session's children from the chevron on its row.
- The URL names the open session, so a reload reopens it.
- Archive a finished session along with the sessions beneath it, show archived
  sessions from the workspace menu, and unarchive them later.
- Delete a session once it has no children. Claude's own transcript and the
  workspace stay on disk.

### Prompts and transcripts

- Pick the harness, model, effort, and permission mode for each new session, or
  start with the configured defaults.
- Send a prompt while Claude works. Claude takes it after its current step, and
  it shows as pending until then.
- Stop a running turn. A pending prompt returns to the composer.
- Replies stream in as Markdown. Tool calls show their name, input, and status,
  and expand to the full input and result.
- Turns Claude starts on its own, from a schedule or a finished background task,
  appear like any other turn.
- Unsent text survives switching sessions and reconnecting.

### Settings

- Change a session's model, effort, or permission mode between turns. The
  conversation carries on with the new settings.
- When Claude changes its own permission mode, as when it leaves plan mode, the
  page shows the new mode.

### Approvals and questions

- Tool permission requests appear as cards above the composer with Allow and
  Deny, or a typed reply.
- Questions from Claude appear as a form, one question at a time, with an Other
  answer.
- An approval waits as long as it needs to, and shows on every open page.

### Sessions working together

Each Claude gets Ox's tools over MCP, with no approval needed outside plan mode:

- List Ox's harnesses, models, effort levels, and permission modes.
- List the other sessions with their workspace, settings, and status.
- Read another session's transcript.
- Message another session. The message reaches it like a prompt, and starts it
  if it is parked.
- Start a child session with a title and a first message. The child inherits its
  parent's settings and directory unless Claude picks others, and can start
  children of its own up to the configured depth.

### Files

- A file tree beside the transcript shows the session's workspace, updating
  within about a second of changes on disk.
- Open a file to view it with syntax highlighting in GitHub's light or dark
  colors. The view follows changes on disk.

### Layout

- Show, hide, and resize the session list, the file tree, and the file viewer.
- On a narrow screen, one pane shows at a time, and the session list and file
  tree slide in from either side.
- Touch screens get tappable rows and a layout clear of notches and browser
  toolbars.

### Persistence and parking

- Workspaces, sessions, settings, and transcripts survive restarts of `oxd`.
- An idle session parks after five minutes, stopping its Claude process, and
  resumes its conversation on the next prompt or message.
- Closing every page leaves prompts, approvals, and Claude's own turns running.
- Every open page shows changes made from any other page as they happen.
- The page reconnects by itself after a dropped connection.

### Harnesses

- **Claude Code**, billed to your Claude subscription, offering Opus, Sonnet,
  and Haiku 5.5 with the manual, auto, accept edits, and plan permission modes.
- **Ur**, Ox's own agent, which runs models through OpenRouter with your
  `OPENROUTER_API_KEY`, offering DeepSeek V4.1 Flash, GLM 5.3 Flash, and Muse
  Spark 1.3 Contributor. Ur runs shell commands in the session's workspace,
  asking for approval first in manual mode and running them freely in auto mode.
  Ur sessions park and resume their conversations like Claude sessions.

### Remote access

- Set `tailscale_ip` in the configuration to also serve the web UI on your
  Tailscale address at port 17840.

## Running Ox

Ox needs Go, Node.js, and Claude Code signed in to your Claude subscription.

```sh
make web build
bin/oxd &
bin/ox
```

Then open <http://localhost:8080/>. `ox -addr` serves the UI on another address,
and `oxd -park-after` changes how long an idle session waits before it parks;
`-park-after=0` turns parking off.

On first start, `oxd` writes its configuration to `~/.config/ox/config.json` for
you to edit. It offers the harnesses and models above, the defaults for new
sessions, and the maximum depth of child sessions.

For development, `make dev` builds Ox with the race detector and runs `oxd`,
`ox`, and Vite together. `make test`, `make e2e`, and `make live` run the
checks.

## Documentation

- [Functional requirements](docs/functional-requirements.md): the behavior Ox
  promises.
- [Architecture](docs/architecture.md): how `oxd` and `ox` are built.
- [Protocol](docs/protocol.md): the JSON-RPC interface `oxd` serves.
- [Claude Code](docs/harnesses/claude-code.md): how Claude Code behaves when
  driven with `claude -p` and stream-json.
- [AGENTS.md](AGENTS.md): instructions for agents working on Ox.

## License

Ox is in the public domain. See [LICENSE](LICENSE).
