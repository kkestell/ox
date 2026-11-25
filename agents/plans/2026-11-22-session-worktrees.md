# Session worktrees

## Goal

A user creating a top-level session can choose to run it in its own git worktree, which Ox makes at creation and removes when the session is deleted. The session's children work in the same worktree by default. This replaces, for Ox's own work, the lead workflow in which an agent runs `make worktree` and sends its children there with `create_session`'s `dir`.

Every session already stores `directory`, fixed at creation, and the file tree follows it (#83). A worktree session stores the worktree's copy of the workspace directory, and its children store the same directory, since a child starts in its parent's directory.

The evidence is `agents/scratch/session-worktrees-history.md` (hypercube's bug classes A–J and git probes) and `agents/scratch/session-worktrees-codex-review.md` (findings 1–12 against the earlier version of this plan). Construction maps each class to what rules it out; the findings are answered under Review findings.

## Related code

- `internal/server/methods.go` `createSession`, `addSession`, `publishLocked`, `listWorkspaces`, `createWorkspace`, `removeWorkspace`: top-level creation, the existing-ID answer, and workspace membership, all under `Server.mu`.
- `internal/server/session.go` `start`, `stop`, `parked`, `destroy`, `end`: the session goroutine's lifecycle. `parked` is the one place an agent is dropped; `destroy` is the only deletion path and today does not wait for the process.
- `internal/server/server.go` `New`, `Close`: startup and graceful shutdown. `Close` waits only for published sessions.
- `internal/server/mcp.go` `createSession`, `addChild`, `checkChildLocked`: child creation. The call goroutine resolves `dir` against the caller's directory; the parent's goroutine checks and publishes.
- `internal/store/store.go`: side tables (`children`, `archived`, `titles`) hold facts only some sessions have. `AddSession` writes a session and its `children` row in one transaction.
- `internal/agent/agent.go` `Agent`; `internal/claude/claude.go` `startProcess`, `process.read` (reaps Claude), `Terminate`; `internal/ur/ur.go` `execute`, `Interrupt`, `Stop`; `internal/ur/shell.go` (`wait` kills the command's process group); `internal/fake/fake.go`.
- `internal/paths/paths.go`, `cmd/oxd/main.go` (`absolute`, the shutdown sequence), `cmd/ox/web/dev.mjs`, `cmd/ox/web/e2e/services.ts`: `oxd`'s path flags.
- `cmd/ox/web/src/NewSessionView.tsx` (`createDraft`, the draft's settings row), `ox.tsx` (`create`, session entries from `workspace/list` and `session/created`), `Sidebar.tsx` (delete dialog), `protocol.ts`.

## Decisions

### Shape

- **Worktree directory.** `paths.Worktrees()` is `$XDG_DATA_HOME/ox/worktrees`, or `~/.local/share/ox/worktrees`. `oxd -worktrees` overrides it; `main` makes it absolute with the existing `absolute` and passes it to `server.New`, which gains a `worktrees string` parameter. `oxd` creates the directory (mode `0o700`) when it makes the first worktree. `dev.mjs` passes `-worktrees=<os.tmpdir()>/ox-dev-worktrees`, outside every checkout; `services.ts` passes `-worktrees=<test dir>/worktrees`, beside the test's database and outside its workspace.
- **One name per attempt.** Each creation with a worktree mints a fresh `name` with `newID()`. Its worktree root is `<worktrees>/<name>` and its branch is `ox/<name>`. Two attempts never share a path or a branch, so two creations with the same `session_id`, or a retry during a checkout, cannot compete for either; each attempt owns what it made until it publishes or discards it.
- **Stored identity.** A new table `worktrees (session_id TEXT PRIMARY KEY REFERENCES sessions ON DELETE CASCADE, worktree TEXT NOT NULL)` holds the worktree's root, as the physical path git reports. `store.Session.Worktree` is that root, empty for a session without one. `AddSession` inserts it in the session's transaction and refuses a record that sets both `Child` and `Worktree` (`a child session cannot have a worktree`). `session.worktree` is fixed at creation. Ownership is read from the row, never derived from the configured directory, so a changed `-worktrees` or XDG variable leaves existing sessions' worktrees owned and removable. Nothing else about git is stored: the branch, HEAD, cleanliness, and registration are read live.
- **Git in one file.** `internal/server/worktree.go` runs every git command through one helper that runs `git` with stdin closed, returns stdout, and turns a failure into `<step>: <git's trimmed stderr>`. It holds:
  - `addWorktree(workspace, worktrees string) (root, directory string, err error)`:
    1. `git -C <workspace> rev-parse --show-prefix`. Its failure is the error for a workspace outside any repository, such as `create worktree: fatal: not a git repository (or any of the parent directories): .git`.
    2. `os.MkdirAll(worktrees, 0o700)`, then `git -C <workspace> worktree list --porcelain -z`. Refuse with `worktree directory <worktrees> is inside the repository at <path>` when the physical `<worktrees>` (`filepath.EvalSymlinks`) lies in any listed worktree.
    3. `git -C <workspace> branch ox/<name> HEAD`. It creates the branch or fails with nothing made, as in an empty repository.
    4. `git -c core.hooksPath=/dev/null -C <workspace> worktree add <worktrees>/<name> ox/<name>`. With hooks off, nothing runs after git's checkout, and git removes a checkout that fails partway itself, so a failure leaves only the branch: run `git branch -D ox/<name>` and join its error.
    5. `root` is `EvalSymlinks(<worktrees>/<name>)`; `directory` is `root` joined with the prefix. Unless `EvalSymlinks(directory)` is a directory equal to or beneath `root`, discard and fail with `<prefix> is not in the worktree` (a workspace folder holding only untracked files, or one HEAD has as a symlink).
  - `discardWorktree(workspace, root string) error`: `git -C <workspace> worktree remove --force <root>`, then `git -C <workspace> branch -D ox/<base(root)>`, errors joined. It is the only `--force`, and runs only before publication, when no process has run in the worktree.
  - `removeWorktree(workspace, root string) error`, below under Deletion.
- **Top-level creation.** `session/create` takes optional `worktree` (boolean). Without it, creation stays in its single `Server.mu` section, unchanged. With it:
  1. Under `Server.mu`: the existing checks, the existing-ID answer, the settings check, and the workspace lookup, then `s.creations.Add(1)`. Capture `w`.
  2. Without the lock: log `session <id>: creating worktree in <worktrees>`, then `addWorktree(w.path, s.worktrees)`.
  3. Under `Server.mu`, in order: refuse `daemon is shutting down` if `s.closed`; refuse `workspace not found` unless `w` is still in `s.workspaces`; if `s.sessions[id]` now exists, answer as step 1 does (`existing` in the same workspace, refused in another); otherwise `addSession` with `Directory: directory, Worktree: root`. Every outcome but a successful `addSession` unlocks, runs `discardWorktree`, and replies with its answer or error, joined with any discard error. `s.creations.Done()` runs once the reply is queued.
  Git never runs under `Server.mu`, so a checkout holds up only its own request. Publication is the one transfer of ownership, decided under the lock that owns membership.
- **Shutdown during a checkout.** `Close` sets `closed` under `Server.mu`, so no creation is admitted after it, then waits for its sessions and for `s.creations`. An admitted checkout finishes, is refused at publication, and is discarded before `oxd` exits. A second stop request exits at once and can leave the worktree and branch of a checkout in progress; the log line from step 2 names the session, and `docs/architecture.md` states the limit.
- **Who works in a worktree.** `(*Server).worktreeOwnerLocked(dir string) *session` returns the session whose worktree contains the physical path `dir`, or nil. It decides the rule in one place for its two callers:
  - `createWorkspace` refuses `path is inside a session's worktree` when the path's physical form has an owner.
  - `create_session` resolves the child's directory to its physical form on the call goroutine. `checkChildLocked` refuses `dir is in another session's worktree` when the owner is not the caller's top-level ancestor. A child without `dir` takes its caller's directory, which passes.
  So only the owner and its descendants are started in a worktree, and the owner is deleted only after its descendants (the existing children rule).
- **Processes end before deletion.** `agent.Agent` gains `Reaped() <-chan struct{}`, closed once every process the agent started has exited. It is called on the session goroutine once the agent has ended.
  - Claude: `process.read`'s wait goroutine closes `reaped` after `cmd.Wait` and `children.Done`. Claude ends its own Bash tool processes when it gets SIGTERM; they run in their own sessions, outside any process group Ox could signal (Claude Code below).
  - Ur: a `sync.WaitGroup` counts shells. `execute` adds before it starts the waiting goroutine, which is done after `wait` has killed the command's process group. `Reaped` returns a channel closed after `Wait`. An interrupted command stays counted until its group is killed and reaped.
  - Fake returns a closed channel; the server's test doubles inherit it.
  - `session.exiting []<-chan struct{}`, owned by `run`: `parked` appends the dropped agent's `Reaped()` and drops entries already closed. It covers stops, parks, archives, and exits alike, since every drop goes through `parked`.
- **Deletion.** `destroy`, on the session's goroutine:
  1. Refuse while the session has children.
  2. `stop` the agent if there is one, then wait on every channel in `exiting`. Every delete waits, so a deleted child's process has exited before its parent's worktree is checked.
  3. If `sess.worktree` is set, `removeWorktree(workspace, root)`:
     - `registered` is whether `git -C <workspace> worktree list --porcelain -z` lists `root`; `present` is whether `root` exists.
     - Registered and present: refuse `worktree HEAD is not on a branch` if `git -C <root> symbolic-ref -q HEAD` exits 1 (any other failure returns git's error); refuse `worktree has uncommitted changes` if `git -C <root> status --porcelain --untracked-files=all --ignore-submodules=none` prints anything. Then `git -c status.showUntrackedFiles=all -C <workspace> worktree remove <root>`, unforced, so git's own refusals, such as a populated submodule, pass through with its text, and its own recheck counts untracked files whatever the repository's `status.showUntrackedFiles`. Ignored files go with the worktree.
     - Registered and missing: `os.MkdirAll(filepath.Dir(root))`, since git cannot resolve a path whose parent is gone, then the same `worktree remove`, which clears the registration.
     - Unregistered and present: log `session <id>: worktree <root> is not registered with git; leaving it` and go on.
     - Neither: nothing to do.
  4. Delete the row, unpublish, and broadcast, as today.
  A refusal or failure in step 3 or 4 leaves the session listed and parked, and the request gets the error. A repeated delete reads the registration and folder again and finishes what is left. Nothing restarts the agent between the check and the removal: only the session's goroutine starts it, and it is in `destroy`.
- **A missing directory.** `session.start` refuses `session directory <dir> no longer exists` when `os.Stat(sess.directory)` is not a directory, before calling the harness. The sender sees the error, as for any launch failure.
- **Protocol.** `session/create` takes `worktree: true`. Session entries in `workspace/list` and `session/created` carry `worktree`, the root, when the session has one.
- **Page.**
  - `createDraft` holds a `worktree` signal, `false` at first, so the choice lasts through navigation and a failed creation. `NewSessionView` shows it as a `Select` named `worktree`, labelled Location, with options Local and Worktree, after Permission mode. `control.create` sends `worktree: true` only when chosen.
  - `ox.tsx` keeps `worktree` on each session entry from both `workspace/list` and `session/created`.
  - The delete dialog reads `This deletes "<label>", its history, and its worktree. Its branch stays.` for a session with `worktree`, and keeps today's text otherwise. Its error shows through the existing request path.

### Concerns

- **Requirements.**
  - **Creating a session**, **Failed creation**: a worktree creation replies after publication or after its discard. A git failure is an ordinary error; the draft gets back its text, images, settings, and Worktree choice.
  - **Lost creation reply**: a retry with the same `session_id` during a checkout makes its own attempt. Whichever publishes first wins; the other discards its worktree and branch and answers `existing`, so the page opens the session with the prompt unsent. A retry after a failed attempt creates afresh. Two pages never share a draft ID.
  - **Removing a workspace**: unchanged. A workspace removed during a checkout makes that creation fail with `workspace not found` and discard.
  - **Deleting a session**, **Archiving a session**: delete as above. Archive and park leave the worktree.
  - **Working directory**, **Ur's shell commands**, **Ur's instructions**: the harness runs in, and Ur reads `AGENTS.md` from, the stored `directory`.
  - **Session header**, **File tree**, **Open file**: they follow `directory` already. A draft's open file stays open in the session; an untracked file absent from the worktree shows why it cannot be read.
  - **Every page in sync**: `worktree` arrives with `session/created` and `workspace/list`.
  - **Independent sessions**: git runs under no lock and on no other session's goroutine. `destroy` runs git on the deleted session's own goroutine.
  - **Stopping oxd**: shutdown waits for an admitted checkout and its discard.
- **Settling.**
  - Each git command ends when git does. Hooks are off for the checkout; the commands are local.
  - `destroy`'s wait ends when Claude exits, at most `killAfter` (10 s) after SIGTERM plus the kill, or when Ur's killed process groups are reaped (`WaitDelay` 1 s).
  - `Close`'s wait on `s.creations` ends when each admitted creation has replied.
  - Every creation path replies exactly once.
- **Process I/O.** Git's stdout is read whole; `worktree list` uses `-z`. Git's trimmed stderr is the error text. Claude's reaping is the existing `cmd.Wait`, which returns at exit because Claude's output pipes are `*os.File`.
- **Construction.**
  - Each attempt's root and branch come from its own fresh name (classes A and G, finding 4).
  - Publication under `Server.mu` decides existence, membership, and shutdown, after the checkout and before any process runs in it (class B).
  - A session has a worktree exactly when its row in `worktrees` exists, written once at creation and only by `session/create`; `AddSession` refuses a child with one (class C, finding 3).
  - The root and the mapped directory are verified before publication (finding 8).
  - Only the owner's family is started in a worktree, decided by `worktreeOwnerLocked` (classes D and F, finding 3).
  - Deletion checks only after every process the session started has been reaped, and removes without `--force` (class D, finding 2).
  - Removal reads registration and presence live, from the workspace, so a missing folder or a half-finished delete is finished by the next delete (class E, finding 7).
  - No setup step, merge, sync, or archive-driven removal (classes H, I, J).
- **Ownership.**
  - `worktrees` row: written by `session/create` with the session row; needed so delete removes the right worktree after a path change; no races, since it is fixed; deleted by cascade with the session.
  - The worktree folder and its registration: the creating request owns them until publication, then the session's goroutine; removed by `destroy`, discarded on every failed creation.
  - Branch `ox/<name>`: the user's from publication; Ox never deletes it after publication. Branches accumulate until the user deletes them.
  - `Server.creations` (`sync.WaitGroup`): owned by `Server`; needed so stopping `oxd` does not orphan a checkout; `Add` runs only under `Server.mu` while not `closed`, and `Close` sets `closed` before it waits; every creation path calls `Done`.
  - `session.exiting`: owned by `run`; needed so delete waits for a process a park, archive, or exit dropped; closed by harness goroutines; pruned at each `parked`.
  - Claude's `process.reaped` and Ur's shell `WaitGroup`: owned by their agents; closed or done by the goroutine that reaps.
- **Claude Code.**
  - Working directory: "Launch": the process runs in the session's directory.
  - Tool processes: live probe on 2.1.331 (Haiku, Claude as a process-group leader): each Bash call, foreground and background, runs in its own session (`setsid`), outside Claude's process group. After SIGTERM to Claude alone, Claude exited in about 0.6 s and both tool processes were gone at 1 s. With stdin closed instead, Claude kept running past 12 s while a foreground tool ran. `docs/harnesses/claude-code.md` does not record this; the `TestLive` case below keeps it, and its result goes into "Stopping". A Claude killed after `killAfter` may leave tool processes Ox cannot reach; that limit goes into `docs/architecture.md`.
  - Project settings in a worktree never trusted: "Behavior of `-p`" records that `-p` skips the trust dialog. The `TestLive` case checks that a committed `.claude/settings.json` applies there.
  - Transcripts: "Transcripts on disk": they land under the worktree's encoded path, and deleting the session leaves them, as today.
- **Trust boundaries.** `oxd` runs git only in a workspace's repository and in worktrees it made. Worktrees hold the user's committed files under the user's data directory, in a parent made `0o700`. The worktree directory is refused inside the source repository. No listener changes.

### Review findings

Each finding from `agents/scratch/session-worktrees-codex-review.md`:

1. Child rollback removing a published child's checkout: **rejected**. Children never own a worktree, so child creation runs no git and discards nothing.
2. Stop channel misses writers: **fixed** by `Reaped` and `session.exiting`, which outlive the agent, and by every delete waiting, so a deleted descendant has exited before the owner's check. Claude's tool processes are Claude's to end on SIGTERM (probe above); a Claude killed after `killAfter` is a stated limit.
3. Ownership by location and writers from outside: **fixed** by the stored `worktrees` row and `worktreeOwnerLocked`.
4. Duplicate ID racing for path and branch: **fixed** by one name per attempt and the existing-ID decision at publication.
5. Failed `worktree add` leaving checkout and branch: **fixed** by `core.hooksPath=/dev/null`; with hooks off, a failed add leaves only the branch, which is deleted.
6. Lost cleanup context and shutdown: **fixed**. Cleanup runs from the workspace, which cannot be removed while it has sessions, and `Close` waits for admitted creations. A second stop request is a stated limit.
7. Delete misrepresenting what remains: **fixed** by reading registration and presence live, removing a missing folder's registration from the workspace, and refusing to start a session whose directory is gone. A repeated delete finishes the work.
8. Placement and prefix: **fixed** by the absolute worktree directory, the containment check against every worktree of the repository, dev and e2e paths outside their checkouts, and the mapped-directory check.
9. Detached HEAD and ignored files: **decided** by the user: detached HEAD refuses; ignored files are deleted.
10. File tree keyed by directory: **rejected**, resolved in #83, which keys the tree and viewer by the selection.
11. Empty directory state: **rejected**, resolved in #83: `AddSession` refuses a directory that is not absolute.
12. Inventory and requirement diff: **fixed** here. `cmd/ox-mcp/main_test.go` gains the `New` argument. A draft starts on Local, so existing specs submit Local without change.

## Check your assumptions

Entries 1–6 are the user's decisions; their wording is for approval. Entries 7–9 follow from the design.

1. **The top-level session owns the worktree; its children share it.** (Decided.)
   - **Plan:** Only sessions the user creates can have a worktree. Their child sessions work in it unless Claude picks another folder, and no other session is started there. Adds **Worktrees** and **Who works in a worktree**; changes **Working directory**, **Ur's shell commands**, **Ur's instructions** (Requirement changes).
2. **A child cannot ask for its own worktree.** (Decided.)
   - **Plan:** Claude's tool for starting a child gains no worktree option. **Claude can start child sessions** is unchanged.
3. **The draft offers Local or Worktree, starting on Local.** (Decided.)
   - **Plan:** Part of **Worktrees**.
4. **Delete refuses to lose work and keeps the branch.** (Decided.)
   - **Plan:** Changes **Deleting a session**. Ignored files go with the worktree. Branches named `ox/…` pile up until the user deletes them.
5. **New worktrees hold only committed files.** (Decided.)
   - **Plan:** Part of **Worktrees**. `.env`, `node_modules`, and other ignored or untracked files are absent; the agent recreates what it needs.
6. **A subfolder workspace opens at the same subfolder.** (Decided.)
   - **Plan:** Part of **Worktrees**. A subfolder holding only untracked files cannot be opened, and creation fails saying so.
7. **Deleting waits for the session's processes to exit.**
   - **Plan:** Every delete, worktree or not, returns once Claude has exited: usually under a second, at most about ten seconds for a Claude that ignores the request to stop. Part of **Deleting a session**.
   - **Alternative:** Wait only for worktree sessions and their children. Faster deletes elsewhere, but a rule that depends on where each session runs.
8. **Stopping `oxd` finishes a worktree being made.**
   - **Plan:** Stopping waits for a checkout in progress and undoes it; a second stop exits at once and can leave that worktree and branch behind. Changes **Stopping oxd**.
   - **Alternative:** Exit at once and always leave it for the user to remove. Faster shutdown, but every stop during a creation leaves debris.
9. **A worktree's branch and folder share a name that is not the session's ID.**
   - **Plan:** The folder and branch are named `ox/<name>` with a fresh name per attempt; the session header shows the folder, so the branch is the one with the same name. That is what makes a retried creation safe.
   - **Alternative:** Name both after the session's ID, which reads better in `git branch` but needs Ox to track creations in progress to keep retries apart.
10. **Ox's own workflow moves to worktree sessions in this change.**
    - **Plan:** `AGENTS.md` "Workflow" changes as under Documentation updates: the user starts each change as a Worktree session in the main checkout's workspace, the top-level agent renames its branch to the slug, and after the merge the user deletes the session instead of the agent running `make prune` from inside the worktree, which would remove the worktree the session runs in.
    - **Alternative:** Land the feature first and change the workflow in a later pull request, keeping `make worktree` until worktree sessions have been used.
    - **Decided:** the Alternative. This change leaves `AGENTS.md` "Workflow" as it is; the "Workflow" items under Documentation updates are dropped. The source-map lines for `worktree.go` and `worktree_test.go` stay.

## Test plan

- Server, new `internal/server/worktree_test.go`, since no existing test builds a git repository. It runs real git in a temp repository whose workspace is a subfolder `sub` with a committed file, with the fake harness and `New` given a temp worktree directory. A `gitGate` helper puts a stand-in `git` first on `PATH` (as the Claude tests' `standIn` does) that, for `worktree add`, waits for a gate file before running the real git. Cases:
  - `session/create` with `worktree: true` lists `directory` `<root>/sub` and `worktree` `<root>` under the worktree directory; branch `ox/<base(root)>` exists; `workspace/list_folder` for the session lists the committed file and not an untracked file put in the workspace.
  - A committed failing `post-checkout` hook (installed in `.git/hooks`) does not run: creation succeeds and the hook's marker file is absent.
  - A non-git workspace fails with `not a git repository`; an empty repository fails; a workspace folder holding only untracked files fails with `is not in the worktree`; a worktree directory inside the repository fails with `is inside the repository`. In each, nothing is listed, `git worktree list` shows only the checkout, no `ox/` branch exists, and the worktree directory holds nothing.
  - Gated: two `session/create` calls with the same ID and workspace: one answers new, the other `existing`; one session is listed, and one worktree and one `ox/` branch remain. Same ID in two workspaces: one is refused, and nothing of its attempt remains.
  - Gated: `workspace/remove` during the checkout succeeds; the creation fails with `workspace not found` and leaves nothing.
  - Gated: `Close` during the checkout does not return until the gate opens; the creation fails with `daemon is shutting down` and leaves nothing.
  - `session/destroy` with an untracked file, with `status.showUntrackedFiles=no` set in the repository, fails with `worktree has uncommitted changes` and the session and file stay. With the file removed and HEAD detached, it fails with `worktree HEAD is not on a branch`. Back on the branch, with an ignored file present, it succeeds: the folder and registration are gone and the branch keeps its commit.
  - Delete after the worktree folder was removed by hand, and after the whole worktree directory was removed, succeeds and clears the registration.
  - A failed row delete after the removal (a `BEFORE DELETE ON sessions` trigger raising an error, added through a second `database/sql` connection to the WAL database) fails the delete; after the trigger is dropped, a repeated delete succeeds.
  - A server restarted over the same store with another worktree directory still lists the session's `worktree`, and deleting it removes the worktree.
  - Sending to a worktree session whose folder was removed by hand fails with `no longer exists`.
  - `workspace/create` on the worktree's `sub` folder fails with `inside a session's worktree`.
  - A session created without `worktree` has no `worktree` field and deletes without git (the existing workspace is not a repository).
- Server, `TestCreateSession` in `internal/server/mcp_test.go`, with a worktree parent built by the helper from `worktree_test.go`: a child without `dir` returns the parent's directory, has no `worktree`, and deleting it leaves the parent's worktree. A child of an unrelated Local session with `dir` set to the worktree's folder, or a symlink to it, is refused with `another session's worktree`; a grandchild of the owner with that `dir` is accepted.
- Server, the `slowStop` double in `mcp_test.go` gains a `Reaped` that closes only after a gate, and a new case in `TestSessionLifecycle` (`methods_test.go`) checks that `session/destroy` after a park does not reply until the gate opens.
- Claude harness, `TestStopKillsClaudeIgnoringSIGTERM` in `internal/claude/prompt_test.go`: right after `Stop`, `Reaped()` is open; it closes once the stand-in is killed.
- Ur, `internal/ur/shell_test.go` or `ur_test.go` beside the existing interrupt case: after `Interrupt` and `Stop` during a running command, `Reaped()` closes only once the command's wait has returned.
- Store, `internal/store/store_test.go`: a session with `Worktree` round-trips through `Workspaces`; `AddSession` refuses a record with `Child` and `Worktree`.
- e2e, new case in `cmd/ox/web/e2e/session.spec.ts` with its own workspace made a git repository with one commit and one untracked file: the draft's Location select shows Local; choosing Worktree and creating shows a header path under the test's worktree directory; the file tree omits the untracked file the draft's tree showed; navigating away and back to a new draft in a non-git workspace with Worktree chosen, creating fails with `not a git repository` and keeps the text and Worktree; the delete dialog mentions the worktree, and delete removes the session.
- Live, new `TestLive` case: a Claude session with `worktree: true` in a temp repository whose committed `.claude/settings.json` sets `env` `OX_PROBE`. The prompt has Claude run `pwd; printenv OX_PROBE` and then start `sh -c 'echo $$ > <tmp>/bg.pid; exec sleep 300'` in the background. The test asserts the tool results contain the worktree directory and the value, then deletes the session and asserts the background process no longer exists when the reply arrives and the worktree is gone.

## Implementation plan

1. `internal/paths/paths.go`: `Worktrees()`. `cmd/oxd/main.go`: the `-worktrees` flag, made absolute, passed through `run` to `server.New`. `cmd/ox/web/dev.mjs` and `cmd/ox/web/e2e/services.ts`: the `-worktrees` paths above.
2. `internal/store/store.go`: the `worktrees` table, `Session.Worktree`, the insert in `AddSession` and its child refusal, and the join in `Workspaces`. Store test.
3. `internal/agent/agent.go`: `Reaped()`. Implement in `internal/claude/claude.go` and `agent.go`, `internal/ur/ur.go`, and `internal/fake/fake.go`. Claude and Ur tests.
4. `internal/server/worktree.go`: the git helper, `addWorktree`, `discardWorktree`, `removeWorktree`.
5. `internal/server/server.go`: `New(…, worktrees)`, `Server.worktrees`, `Server.creations`, `Close` waiting for it, `worktreeOwnerLocked`.
6. `internal/server/session.go`: `session.worktree` from the record, `exiting` in `parked`, the directory check in `start`, `destroy` as decided.
7. `internal/server/methods.go`: `createSession`'s worktree path, `worktree` on session entries in `listWorkspaces` and `publishLocked`, the refusal in `createWorkspace`.
8. `internal/server/mcp.go`: the physical child directory on the call goroutine, and the owner check in `checkChildLocked`.
9. Every `New` and `newServer` call: `newServer` passes `os.TempDir()`, and `worktree_test.go` calls `New` with its own directory; `cmd/ox-mcp/main_test.go` passes `t.TempDir()`. Then the server tests above.
10. Web client: `protocol.ts`, `ox.tsx`, `NewSessionView.tsx`, `Sidebar.tsx` as decided. The e2e case.
11. The `TestLive` case, and its result in `docs/harnesses/claude-code.md`.
12. Documentation, below. `make docs`.

## Requirement changes

- **Added:** **Worktrees.** Before sending, the user can choose to run a new session in the workspace directory, the choice a draft starts with, or in its own git worktree. Ox makes the worktree outside the repository, on a new branch from the workspace's current commit, holding only committed files, and runs the session in the worktree's copy of the workspace directory; if it cannot, creation fails with the reason and leaves no worktree or branch behind.
- **Added:** **Who works in a worktree.** A worktree session's child sessions work in its worktree unless Claude picks another directory. No other session is started there: Ox refuses a child of another session in it and refuses to add a folder inside it as a workspace, and says why.
- **Changed:** **Deleting a session.**
  - Old: "A session, archived or not, can be deleted only when it has no child sessions. Deleting it asks first, then stops Claude, deletes Ox's history of it, leaves Claude's transcript, Ur's saved conversation, and the workspace on disk, and removes it from every page."
  - New: "A session, archived or not, can be deleted only when it has no child sessions. Deleting it asks first, then stops Claude and waits for it to exit, deletes Ox's history of it, removes its worktree if it has one, leaves Claude's transcript, Ur's saved conversation, the session's branch, and the workspace on disk, and removes it from every page. A session whose worktree has uncommitted changes or untracked files, or whose worktree is on no branch, is not deleted, and the error says why; files Git ignores are deleted with the worktree, and a worktree folder already removed does not stop the deletion."
- **Changed:** **Working directory.**
  - Old: "Claude works in the session's workspace directory, or in a child session, the directory it was created in."
  - New: "Claude works in the session's directory: the workspace directory, its copy in the session's worktree, or for a child session, the directory it was created in. A session whose directory no longer exists refuses to start, and says so."
- **Changed:** **Ur's shell commands.** "Ur can run shell commands in the session's workspace directory" becomes "Ur can run shell commands in the session's directory". The rest is unchanged.
- **Changed:** **Ur's instructions.**
  - Old: "Ur knows the session's workspace directory, its OS, and today's date, follows Ox's style, and follows the workspace's `AGENTS.md` as it stands when each request is sent."
  - New: "Ur knows the session's directory, its OS, and today's date, follows Ox's style, and follows the `AGENTS.md` in that directory as it stands when each request is sent."
- **Changed:** **File locations.**
  - Old: "Ox reads its configuration and keeps its database, socket, and Ur's saved conversations in the user's XDG directories, and the user can choose other paths."
  - New: "Ox reads its configuration and keeps its database, socket, Ur's saved conversations, and session worktrees in the user's XDG directories, and the user can choose other paths."
- **Changed:** **Ox's own files.**
  - Old: "Ox's own bookkeeping writes only its database, its socket, the configuration it writes on first start, and Ur's saved conversations."
  - New: "Ox's own bookkeeping writes only its database, its socket, the configuration it writes on first start, and Ur's saved conversations. In the user's repositories, Ox writes only the worktrees and branches of worktree sessions."
- **Changed:** **Stopping oxd.**
  - Old: "Stopping `oxd` stops every Claude process and Ur command it runs; a second stop request ends it at once."
  - New: "Stopping `oxd` stops every Claude process and Ur command it runs, and undoes a worktree it is still making; a second stop request ends it at once, which can leave that worktree and its branch behind."

## Documentation updates

- `docs/protocol.md`:
  - `session/create`: `worktree`, its errors, the discard, and the repeat-ID answer during a checkout.
  - Session entries: `worktree`.
  - `session/destroy`: it waits for the process to exit, removes the worktree, and its refusals.
  - `workspace/create` and `create_session`: the worktree refusals.
- `docs/architecture.md`:
  - `paths` row: the worktree directory.
  - Sessions: the stored worktree, creation's three steps and publication, `Close` waiting for creations, `worktreeOwnerLocked`, `Reaped` and `exiting`, `destroy`'s order, and the limits: a second stop during a checkout, and tool processes of a Claude killed after `killAfter`.
- `docs/harnesses/claude-code.md` "Stopping": Bash tool processes run in their own sessions; SIGTERM ends them with Claude; closing stdin does not end Claude while a foreground tool runs (2.1.331).
- `AGENTS.md` source map: `internal/server/worktree.go  # session worktrees` and `worktree_test.go  # worktree tests`.
- `AGENTS.md` "Workflow", if entry 10 stands:
  - "Each change starts with `make worktree NAME=<slug>` from a clean main checkout, and every step below runs in that worktree." becomes "Each change starts in a top-level session the user creates in the main checkout's workspace, from a clean, pulled `main`, with Worktree chosen. The top-level agent first runs `git branch -m <slug>`, and every step below runs in that worktree, which its child sessions share."
  - Step 8's "After the user merges, the top-level agent runs `make prune` and pulls in the main checkout." becomes "After the user merges, the top-level agent pulls in the main checkout, and the user deletes its session, which removes the worktree and keeps the branch."
  - The top-level agent sends the new text to every other top-level agent with a change in flight.
