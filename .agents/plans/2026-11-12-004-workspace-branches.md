# oxd lists a workspace's branches, fetching in the background, and says which workspaces are in Git

- Task: `2026-11-12-004-workspace-branches` (numbered 007 before the
  renumbering)
- Status: ready (revision 3). Revision 3 adds the git-reason refusal fix
  described below, at the user's decision on a review-004 finding.
  Revision 2 absorbed the former shared design's
  sections on branch listing, fetch and the Git working tree flag, along with
  their protocol items, edge cases and settled entries. The wording is
  unchanged, and the design was then deleted.
- Source HEAD: planned at `e7fdf22c675479f9f2fa328afe285c173ca99618`; executes
  on the commit of `2026-11-12-003-create-session-worktrees`
- Author: planner child of Hypercube session 2ea441ae, Claude Code, Opus 5.5
  (claude-opus-5-5); coordinator ID `planner`; native conversation ID
  `5ce61adf-7d6c-4b85-ab78-5167733b9e23`
- Dependencies: `2026-11-12-003-create-session-worktrees`

## Outcome and boundaries

This is correction E (background fetch) and correction F (hide worktree items
outside Git), daemon side, of the session checkout request recorded in
`2026-11-12-003-create-session-worktrees`.

`workspace.branches { workspace_id, fetch, env }` answers a workspace's local
and remote-tracking branches. With `fetch: true`, it first runs a background
`git fetch --all`, which `oxd` coalesces per repository and stops after 60
seconds. A failure is reported in `fetch_error` beside the unchanged lists.
Every `WorkspaceInfo` `oxd` sends carries `git_working_tree`, computed when the
listing is built.

This unit also carries a fix to 003's committed code. When git's repository
check fails, the `INVALID_REQUEST` refusal keeps its code and message and
appends git's trimmed stderr: "`<path>` is not in a Git working tree: <git's
message>". The fix applies to `worktree::plan`'s `rev-parse --show-toplevel
--show-prefix` and to `branches::common_dir`'s `rev-parse --show-toplevel
--git-common-dir`. This makes the code match the confirmed behaviour that a
`.git` git rejects (dubious ownership, a broken `gitdir:` file) fails with
git's reason.

Not in this unit:

- the dialog that uses them (006);
- session creation, which never waits on a fetch (003).

## Contract relied on from 003

- **The git runner** in `crates/oxd/src/worktree.rs`. `Git::for_client(env)`
  is `oxd`'s environment overlaid with the request's `env`, without the
  repository-local `GIT_*` variables, and with `GIT_TERMINAL_PROMPT=0`. It
  runs `git -C <dir>` with stdin null and returns stdout, or an error carrying
  git's stderr.
- **Requests that do Git work run in their own tasks** in `conn.rs`, so a client
  disconnecting never cancels them. This unit adds `workspace.branches` to that
  arm.
- **Outside a Git working tree, `INVALID_REQUEST` "`<path>` is not in a Git
  working tree: <git's trimmed stderr>"**, decided by `rev-parse
  --show-toplevel`. As committed, 003 omits git's message. This unit makes it
  match the amended contract (003's revision 4), as described below.
- **Protocol version stays 1**, and every caller is updated.

## Current system

As left by 003, plus:

- `WorkspaceInfo { id, path }` is both the wire type and what
  `Store::workspaces` and `add_workspace` use. The registry holds
  `Registry.workspaces: Vec<WorkspaceInfo>`, and `Daemon::list` clones it into
  each `Listing`.
- `Daemon::client()` signals `changed` when a client connects, so a new watch
  rebuilds the listing.
- `oxd` depends on `libc`.

## Design

### Protocol

```rust
#[serde(rename = "workspace.branches")]
WorkspaceBranches { workspace_id: String, #[serde(default)] fetch: bool,
                    #[serde(default)] env: BTreeMap<String, String> },
// answers:
pub struct Branches { pub local: Vec<String>, pub remote: Vec<String>,
                      pub fetch_error: Option<String> }

// WorkspaceInfo gains:
pub git_working_tree: bool,
```

### Branch listing and fetch

`workspace.branches { fetch: false }` answers at once with
`for-each-ref --sort=-committerdate refs/heads refs/remotes`. Symbolic refs
such as `origin/HEAD` are left out, and `fetch_error` is `None`.

`workspace.branches { fetch: true }` first runs `git fetch --all --quiet`, then
lists:

- It fetches all remotes, because the suggestions show every remote.
- It passes no `--prune`, so the user's `fetch.prune` settings decide.
- A fetch failure answers the list as it stands, with `fetch_error: Some(git's
  message)`.
- A request that is not in a Git working tree answers `INVALID_REQUEST`, with
  git's reason appended.

Fetches are coalesced per repository. `oxd` keeps a map from the absolute
common Git directory to an in-flight fetch: a `watch` channel carrying its
outcome. A request that finds one in flight waits for that outcome instead of
starting another. Concurrent opens of the dialog therefore share one fetch, and
git never races itself on remote-tracking refs. The coalesced fetch uses the
environment of the request that started it.

A fetch runs in its own process group, with a 60-second timeout on the wait.
On timeout, `oxd` sends `SIGTERM` to the group, which lets git remove its lock
files, then waits for the process. The outcome is "git fetch did not finish
within 60 seconds". Session creation never waits on a fetch.

### Git working tree flag

`Daemon::list` computes `WorkspaceInfo.git_working_tree` each time it builds a
listing, without running git. It is true when the workspace path has no `.git`
component and the path or an ancestor contains a `.git` entry (a directory, or
a linked worktree's file). The registry and store hold workspaces without the
flag. A later `git init` shows in the next listing `oxd` sends: the next session
change or client connection. If git rejects the `.git`, the app still shows the
worktree items, and `session.new` then refuses with git's reason.

### Edge cases

| Case | Behaviour |
| --- | --- |
| Not a Git repo, bare repo, inside `.git` | Flag false (the app hides the worktree items); `workspace.branches` refuses. |
| `.git` present but rejected by git | Flag true; `session.new` and `workspace.branches` refuse with git's reason. |
| Fetch hangs or fails | Timeout or error note; list unchanged; creation unaffected. |

### Structure

- **Types**: `Store` and the registry hold a new `SavedWorkspace { id, path }`.
  `WorkspaceInfo` is built only by `Daemon::list` and `add_workspace`, via
  `branches::git_working_tree(path)`, the pure filesystem check described
  above.
- **`crates/oxd/src/branches.rs`**:
  - `list(git, workspace) -> Result<(Vec, Vec), ErrorBody>` runs
    `for-each-ref --sort=-committerdate --format=%(refname)%00%(symref)
    refs/heads refs/remotes`, drops symbolic refs, and strips the prefixes.
  - `fetch(git, workspace) -> Result<(), String>` runs `git fetch --all
    --quiet` through the runner with `process_group(0)`, and waits under
    `tokio::time::timeout(60 s)`. On timeout it sends `libc::killpg(pid,
    SIGTERM)`, awaits the child, and returns the timeout message.
  - The runner gains whatever option these two settings need; every other git
    call keeps the runner's defaults.
- **Coalescing**: `Daemon.fetches: Mutex<HashMap<PathBuf,
  watch::Receiver<Option<Result<(), String>>>>>`, keyed by `rev-parse
  --path-format=absolute --git-common-dir`. A request takes the existing
  receiver, or inserts a new one and spawns the fetch task. That task sends the
  outcome, then removes the entry under the lock. Waiters await
  `wait_for(Option::is_some)`. Because the fetch runs in its own task, a
  dropped request does not cancel it.
- **Refusal with git's reason**: `worktree::not_a_working_tree(workspace,
  stderr)` formats "`<path>` is not in a Git working tree: <stderr>" from the
  `GitError`'s trimmed stderr. Both `worktree::plan` and
  `branches::common_dir` pass the error they receive, instead of discarding it
  with `map_err(|_| ..)`. `common_dir`'s second refusal (empty output, no
  stderr) keeps the bare message.
- **Handler**: resolves the workspace (`NOT_FOUND`) and answers
  `INVALID_REQUEST` outside a Git working tree. With `fetch`, it awaits the
  coalesced outcome first, then lists. `conn.rs` adds `WorkspaceBranches` to
  the spawned arm.

## Check your assumptions

Both entries are settled; they were numbered 10 and 11 among the entries for
the whole request.

1. **Confirmed.** **The background fetch is `git fetch --all`, uses your prune
   settings, runs at most once per repository at a time, and is stopped after
   60 seconds.** Alternative: fetch only the default remote, which is faster
   with many remotes but leaves other remotes' suggestions stale.
2. **Confirmed.** **Worktree items for a workspace appear after `git init` only
   when `oxd` next sends the lists** (the next session change, client
   connection or app start), not the moment the repository exists. The flag
   looks for `.git` rather than running git.

## Implementation sequence

1. **Protocol**: `Method::WorkspaceBranches`, `Branches`, and
   `WorkspaceInfo.git_working_tree`; `Client::branches(workspace_id, fetch,
   env) -> Branches`; round-trip tests. Update the callers that construct
   `WorkspaceInfo`: the protocol tests, the `daemon.rs` tests, and the ox app
   if it does.
2. **Store and registry**: `SavedWorkspace`; the `Store` methods take and
   return it, and the store tests follow.
3. **`branches.rs`**, as in **Structure**.
4. **Daemon and conn**: `fetches`, the handler, `list` and `add_workspace`
   building `WorkspaceInfo`, and the spawned arm.
5. **Refusal with git's reason**: `not_a_working_tree` takes git's stderr,
   and `worktree::plan` and `branches::common_dir` pass it through, as in
   **Structure**. Update 003's tests that compare the refusal's message
   exactly, if any, to the new form.
6. **Docs**: `docs/architecture.md` describes the flag and `workspace.branches`
   with fetch. Add doc comments on the new items.

## Verification

Add to `crates/oxd/tests/worktrees.rs`, using 003's harness and fixtures. The
remote is a local bare repository.

- **Without fetch**: local and remote lists, most recent commit first, no
  `origin/HEAD`, `fetch_error: None`. A plain-directory workspace →
  `INVALID_REQUEST`.
- **With fetch**: a branch pushed to the remote after the clone is absent
  without fetch and present with it.
- **Fetch failure** (the clone's `remote.origin.url` points at a missing path):
  `fetch_error` is set, and the lists equal the unfetched ones.
- **Coalescing**: the clone's `remote.origin.uploadpack` points at a script the
  test writes into its tempdir. The script appends a line to a log, reads a
  line from a FIFO (blocking until the test writes it), then `exec`s `git
  upload-pack "$@"`. The test sends one `fetch: true` request and waits for
  the log line, which confirms the fetch is in flight. It then sends a second
  request, writes the FIFO, and checks that both answers succeed and the log
  has exactly one line.
- **Client environment**: a fetch fails when the client env's
  `GIT_CONFIG_GLOBAL` names a config that sets `url.<missing>.insteadOf` to the
  remote's URL. The same fetch without that env succeeds.
- **Flag**: a Git workspace, a subdirectory of one, and a plain directory list
  `true`, `true` and `false`. After `git init` in the plain directory and a new
  client connection, the listing shows `true`.
- **`.git` that git rejects**: the workspace holds a `.git` file containing
  `gitdir: <tempdir>/missing`. The listing reports `git_working_tree: true`.
  `session.new` with a worktree checkout and `workspace.branches` both answer
  `INVALID_REQUEST`, and both messages contain "is not in a Git working tree:"
  followed by git's "gitfile does not point to a valid repository". Undo
  check: dropping the stderr from either call makes the test fail.
- **Undo check** (AGENTS.md): removing the coalescing makes the coalescing test
  log two lines.
- **Not tested**: the 60-second timeout and its `SIGTERM`. A 60-second test is
  too slow, and AGENTS.md rules out a test-only timeout flag. The work log
  names this.

Commands: `make check-rust`, then `make smoke-claude`.

## Handoff

Ready once 003 is committed. Size: one normal assignment, made of a listing
method with one concurrency mechanism (coalescing with a timeout) and a
computed flag that changes the workspace types, plus a two-call-site error
fix to 003's code with one test. Detail: the plan now carries
its own contract, which was formerly in the shared design. `plan_metrics.py`:
1,494 words (37 fenced) at revision 2, inside the band. At revision 3 it is
1,780 words, above the band; the added length is the refusal fix's scope and
test. The work grew only by that fix.
