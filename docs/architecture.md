# Architecture

Ox runs as a tree of processes. The desktop app and `oxctl` are clients of
`oxd`, a per-user daemon that owns every session's agent process. Sessions
outlive the clients that started them, so closing and reopening the app
reconnects to the same sessions. `oxd` also saves its workspaces and sessions,
so they outlive the daemon.

```mermaid
flowchart TB
    ox["ox"]
    oxctl["oxctl"]
    oxd["oxd"]
    bridge["claude-code-acp<br/>(one per session)"]
    claude["claude<br/>(one per session)"]

    ox -.-> oxd
    oxctl -.-> oxd
    oxd --> bridge
    bridge --> claude
```

Solid arrows point from a parent to the processes it owns. Dotted arrows
start `oxd` detached, so it outlives the client that started it.

## Processes

**ox.** The Tauri 2 desktop app. The React UI in the webview calls Tauri commands
in the Rust core, which forwards them to `oxd` through one
`ox_protocol::Client` and streams the workspaces and sessions lists and the
open session's conversation back over Tauri channels. The sidebar groups
sessions under their workspaces; a workspace is added by choosing a directory.
A workspace's New Session menu opens beside its button with an agent select
above **Existing Checkout**,
which runs the session in the workspace's directory, and, in a Git working
tree, **New Worktree**, on a new branch, and **New Worktree on Branch…**, which
asks for a branch and, for a new branch, the commit it starts from. That
dialog suggests the workspace's branches at once, then again once a fetch of
its remotes answers, and notes a failed fetch. Selecting a saved session whose
agent is not running loads it. A new session runs `claude-code-acp` from
beside the app's executable. The app passes its environment to the session
and to every request that runs git. **Delete Session** asks before discarding
a worktree's unsaved work, says which branch `oxd` kept, and counts a session
that is already gone as deleted. The app's dialogs are in-window `<dialog>`
elements; errors show in a native message box.

**oxctl.** A command-line client of `oxd` that speaks the same protocol as
the app. `oxctl new --worktree` starts a session in a new worktree, on a
branch `--branch` names, started at `--start`. `oxctl delete` deletes a
session, and with `--discard` gives up its worktree's unsaved work; it prints
the branch it kept, if any.

**oxd.** A per-user daemon on `$OX_SOCKET`, or `oxd.sock` in Ox's data
directory: `$XDG_DATA_HOME/ox` when `XDG_DATA_HOME` is an absolute path, else
`~/.local/share/ox`.
`Client::connect_or_start` starts `oxd` from beside the client's executable
when no daemon answers, in a new session (`setsid`) with stderr appended to
`oxd.log` beside the socket, and reads the `ready` line it prints once a
daemon is listening. A startup lock and an instance lock keep one daemon per
socket. `oxd` accepts connections only from its own user, and exits after an
idle period with no clients and no running agents; a session whose agent has
exited is saved and does not keep `oxd` alive. Each session runs its agent
command in its own process group and speaks ACP to it over stdio; closing the
session kills that group.

`oxd` speaks draft ACP protocol v2 to its agents; [`acp-v2.md`](acp-v2.md)
charts its support. A turn starts and ends on the agent's `state_update`
session updates, whether a prompt or the agent started it. A prompt makes the
session running at once and records `prompt_started`; `running`,
`requires_action`, and a state `oxd` does not know start a turn the agent
began by itself, recorded as `turn_started`. The agent's `idle` ends the
turn, recorded as `turn_ended` with the stop reason it reports, and `oxd`
takes no prompt until then. After a prompt, only an `idle` that follows the
agent reporting work ends the turn, since the agent may repeat an idle
snapshot first. The agent answers `session/prompt` when it inserts the
prompt; an error answer means it did not, and ends the turn with that error
unless the agent already reported work for the prompt. `oxd` opens a session
only with an agent that advertises `sessionCapabilities`.

**Workspaces and saved sessions.** A workspace is a directory the user
added. A session belongs to a workspace by ID and runs in the workspace's
directory or in a worktree of it, a directory the session stores
separately. `workspace.remove` is refused
while any session, exited ones included, belongs to the workspace. `oxd`
saves workspaces and sessions in one SQLite database beside the socket,
`<socket>.db`, and is the only process that writes it. A session's row holds
its workspace, command, directory, title, and the agent's session ID, and is
written when the session is created and again when its title or agent session
ID changes. The database holds no environment variables: a client passes its
environment each time it starts an agent, with `session.new` and
`session.load`. The schema has no migrations.

**Worktrees.** `session.new` takes a `checkout`: the workspace's directory, or
a new worktree of the workspace's Git repository. `oxd` runs every Git
operation as the `git` CLI, with the client's environment over its own and
without the variables that point git at one repository (`GIT_DIR` and the
rest of `git rev-parse --local-env-vars`). A worktree's branch is one of
three kinds:

- **New**: with no name, `ox/<first 8 digits of the session ID>`, or a name
  that is neither a local nor a remote branch. It starts at the request's
  `start`, or `HEAD`, and has no upstream.
- **Existing**: a local branch, which is refused while any worktree has it
  checked out.
- **Track**: a name found only on a remote, as `<branch>` or
  `<remote>/<branch>`, which becomes a local branch tracking it. A name on
  several remotes takes the one `checkout.defaultRemote` names, and is
  otherwise refused.

`start` applies only to a new branch. The worktree is
`worktrees/<checkout name>-<first 8 digits of the session ID>` beside the
socket, where the checkout name is the final component of the top level of
the workspace's checkout, and the session runs in the same subdirectory of
it that the workspace is of its checkout. It holds committed files only;
uncommitted changes stay in the workspace, and `oxd` writes nothing into the
repository but the worktree's administrative files and the branch.

Creation has two phases. Before answering, `oxd` resolves and checks the
branch, refuses a worktree directory that already exists, creates a new or
tracking branch with `git branch`, which fails if the branch exists, and
saves the session's row and its `worktrees` row (path, branch, the commit
it started at, whether `oxd` created the branch, and the repository's
absolute common Git directory) in one transaction. Any
failure there answers with an error and leaves nothing behind. The answer is
the session in `starting`. The session's startup then runs `git worktree
add` and launches the agent in the worktree. If the worktree cannot be added,
`oxd` removes what git left, deletes a branch it created if the branch still
points where it started, and forgets the worktree; the session is `exited`
with git's error and, never having started, is deleted at the next start of
`oxd`. `workspace.remove` is refused while the database has a session row for
the workspace, which covers a session being created. `session.load` refuses
a session whose directory no longer exists.

`session.delete` deletes a session. For a session in a worktree it first
waits for the session's startup to finish; a load or another delete of the
session waits for it in turn. Unless the request sets `discard`, it refuses
with `UNSAVED_WORK`, listing what would be lost and leaving the session as it
was, while the worktree has uncommitted changes (`git status`, untracked
files included, ignored files not), commits on no branch, remote branch or
tag, or an initialized submodule has commits on no remote branch. It also
refuses while git cannot use the worktree, so that nothing can be checked:
its repository is gone, or the repository no longer knows the worktree. It
then stops the agent and waits for its task to end, and removes the
worktree, ignored files included, with `git worktree remove --force`. A
worktree git cannot use is deleted as a directory, which `oxd` owns, and a
worktree directory deleted outside Ox is unregistered from the repository.
Last it applies the branch rule: a branch `oxd` created is deleted, at the
tip it read, when every commit on it since the session's starting commit is
on another branch, a remote branch or a tag, and no worktree has it checked
out. Otherwise the branch is kept and named in the answer's `kept_branch`,
as it is when the repository is gone and the branch cannot be checked. A
branch that existed before the session is never deleted. Removal and the
branch rule run git in the recorded common Git directory with `--git-dir`
set to it, and the unsaved checks run in the worktree, so none of them
depends on the workspace's directory still existing. If work appears
after the check, or git fails, such as on a locked worktree, the delete
answers the error and the session stays `exited` and loadable. Deleting a
session not in a worktree stops its agent and deletes its row.

Each workspace `oxd` sends carries `git_working_tree`, which says whether to
offer worktrees for it. `oxd` works it out from the filesystem each time it
builds the lists, without running git: it is true when no component of the
workspace's path is `.git` and the path or one of its parents has a `.git`
entry. A `git init` therefore shows in the next lists `oxd` sends, on the
next session change or client connection. `workspace.branches` answers the
local and remote-tracking branches of a workspace's repository, each most
recently committed first, without symbolic refs such as `origin/HEAD`.
`workspace.branches` and `session.new` with a worktree refuse a workspace
git finds in no working tree, including one whose `.git` git rejects, with
git's reason in the message. With `fetch`, `oxd` first runs `git
fetch --all`, which follows the user's prune settings. Requests for one
repository, keyed by its common Git directory, share the fetch running for
it, which uses the environment of the request that started it and runs in a
task of its own. A fetch runs in its own process group and is sent
`SIGTERM` after 60 seconds. A fetch that fails or is stopped is reported in
`fetch_error`, beside the lists as they stand. Session creation never waits
on a fetch.

Shutdown on SIGTERM, SIGINT, or idle exit kills the agents and keeps every
row. When `oxd` starts it registers each saved session in state `exited`,
with no agent and no events, and deletes the sessions that never started,
which have no agent session ID to load, and have no worktree. After it
reports `ready`, a background task cleans up those that never started in a
worktree, one at a time, holding each one's lifecycle lock, so that a load
or delete of it waits. It removes the worktree and applies the branch rule
with `oxd`'s own environment, as a delete without `discard` does, and then
deletes the session. A session whose worktree would lose work, or cannot be
removed, is kept as `exited`, with the reason in `oxd`'s log, and can only
be deleted. `oxd` does not idle-exit while this runs, and a shutdown does
not wait for it; sessions it has not reached are cleaned up at the next
start.
`session.load` restarts the agent of an exited session that has an agent
session ID, from the session's stored command and directory, on the same
session: the session ID, subscriptions, and sidebar selection stay the same,
and the session goes through `starting` to `idle` as a new one does. The agent
replays the conversation, then reports its state, before it answers
`session/resume` with `replayFrom: start`. An agent that reports it is working
leaves the session `running` until its `idle`, with no `turn_started` or
`turn_ended` for work the history did not see start. A session whose
agent has not started yet records the replay as its history once the agent
starts, so a load that fails partway records none of it; one whose agent
has started, such as one that crashed, keeps its history and drops the
replay, and its history shows a second `agent_started` after the
`agent_exited`.

**claude-code-acp.** An ACP agent, speaking draft protocol v2, that bridges to
the Claude Code CLI. [`acp-v2.md`](acp-v2.md) charts its support of the
protocol. A session's first prompt starts one `claude -p`, in the bridge's
process group, and the session keeps it: the bridge holds its stdin open,
writes each prompt to it as a stream-json message, reads streamed output and
permission requests from its stdout, and answers permission requests with the
client's decision. A prompt is answered when Claude takes it into a turn: the
bridge sends it as a user message and answers with that message's ID. The
turn's output, and its end as a `state_update` of `idle` with a stop reason,
arrive as updates. A subagent's tool calls show as lines of the content of
the tool call that started it, and a tool call's result as its content.
`session/cancel` interrupts the running turn, and a model or mode change goes
to the running process as a control request. Claude keeps running between
prompts, and the turns it starts by itself, when a background task reports
back, stream to the client as a prompt's turn does. When `claude` has exited,
the next prompt starts another with `--resume` and the Claude session ID.
`session/close` stops `claude`. The bridge records its sessions in
`acp-sessions.json` and replays Claude's transcripts on `session/resume` with
`replayFrom: start`.

**claude.** The Claude Code CLI, authenticated with the user's subscription.
