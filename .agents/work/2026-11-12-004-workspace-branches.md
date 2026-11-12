# oxd lists a workspace's branches, fetching in the background, and says which workspaces are in Git

- Task: `2026-11-12-004-workspace-branches`
- Status: complete
- Plan: `.agents/plans/2026-11-12-004-workspace-branches.md` (repository-relative), blob `5d7b58ab51ee2b210c027c5bfdce45697576fff6`
- Source HEAD: `815240a1883eb4e4dd508a1547efb26cbb07c8b6` (unit 003 committed)
- Review: `.agents/reviews/2026-11-12-004-workspace-branches.md` (rounds 1–2)

## Changes

The design is the plan's; this lists where it landed.

- Protocol (`crates/ox-protocol`): `Method::WorkspaceBranches { workspace_id,
  fetch, env }` (`fetch` and `env` default), `Branches { local, remote,
  fetch_error }`, `WorkspaceInfo.git_working_tree`, `Client::branches`.
  Round-trip tests for the request with and without its defaults, `Branches`,
  and a listing carrying the flag.
- Store (`crates/oxd/src/store.rs`): `SavedWorkspace { id, path }` replaces
  `WorkspaceInfo` in `workspaces` and `add_workspace`, and in the store tests.
- Runner (`crates/oxd/src/worktree.rs`): `Git` is `Clone`; `Git::command`
  builds the configured `Command` that `run` executes, so `fetch` can add
  `process_group(0)` and its own wait; `run` is public to the crate;
  `not_a_working_tree` is shared with `branches.rs`.
- `crates/oxd/src/branches.rs` (new): `git_working_tree` (the filesystem
  check), `common_dir` (`rev-parse --path-format=absolute --show-toplevel
  --git-common-dir`, refusing outside a working tree), `list`
  (`for-each-ref --sort=-committerdate`, symbolic refs dropped), `fetch`
  (`git fetch --all --quiet` in its own process group, 60-second timeout,
  `killpg(SIGTERM)` then wait).
- Daemon (`crates/oxd/src/daemon.rs`): the registry holds `SavedWorkspace`;
  `list` and `add_workspace` build `WorkspaceInfo` with the flag;
  `fetches` map keyed by common Git directory; `branches` handler (always
  checks the working tree, then the coalesced fetch when asked, then lists);
  `workspace_path` helper shared with `new_session`.
- `crates/oxd/src/conn.rs`: `workspace.branches` runs in its own task.
- Tests (`crates/oxd/tests/worktrees.rs`): seven new tests (below).
  `BlockedGit` gains `after(pattern)`, which stops once the command has run;
  its FIFO helpers became free functions shared with the new `UploadPack`
  fixture; `workspace_id` is shared by `new_session_with` and `branches`;
  `refusal` is generic over the result type.
- Docs: `docs/architecture.md`, a paragraph on the flag and
  `workspace.branches` after the worktree paragraphs.

Changed paths:

- `crates/ox-protocol/src/client.rs`
- `crates/ox-protocol/src/messages.rs`
- `crates/oxd/src/branches.rs` (new)
- `crates/oxd/src/conn.rs`
- `crates/oxd/src/daemon.rs`
- `crates/oxd/src/main.rs`
- `crates/oxd/src/store.rs`
- `crates/oxd/src/worktree.rs`
- `crates/oxd/tests/worktrees.rs`
- `docs/architecture.md`
- `.agents/work/2026-11-12-004-workspace-branches.md` (this log)

## Deviations

- **The fetch's error is git's stderr alone**, not the runner's `GitError`
  text ("git fetch --all --quiet failed: …"), since the plan calls it "git's
  message" and the app shows it as a note.
- **The coalescing test confirms the second request has looked up the
  repository before releasing the first fetch.** The plan's test sends the
  second request and writes the FIFO at once, which can let the first fetch
  finish before the second request reaches the coalescing map, so the second
  starts its own fetch and the test fails with coalescing in place. The test
  gives the second request a `BlockedGit::after(" rev-parse ")` `PATH`, and
  releases the first fetch only after that request's `rev-parse
  --git-common-dir` has run. What remains between that point and the map
  lookup is the wrapper exiting and the task taking a mutex, while the first
  fetch still has to run upload-pack and exit; that ordering is assumed, not
  enforced.
- **The flag test adds a workspace inside `.git`** (expected false), so that
  the `.git`-component rule has a test that fails without it.
- **`crates/ox` and `ui/src/protocol.ts` are unchanged.** The app constructs
  no `WorkspaceInfo`; it forwards the new field to the UI, and 006's plan adds
  it to `protocol.ts`.

## Verification

Environment: macOS (Darwin 25.6.0), rustc 1.96.0, git 2.56.0; checkout
`/Users/kyle/projects/ox/.hypercube/2ea441ae` on `hypercube/session-2ea441ae`
at `815240a` plus this unit's uncommitted changes.

| Command | Result | Evidence |
| --- | --- | --- |
| `make check` (`check-rust`, `check-ui`, `check-scripts`) | passed: fmt, clippy `-D warnings`, all workspace tests (`worktrees` 25 passed, `daemon` 47, `ox-protocol` 11, store/state 44, others), Biome, `tsc`, ruff | final run after the last edit; output was in `/tmp/ox-004-check.log`, summarized in the native conversation |
| `make smoke-claude` | passed: every check through `close_and_idle_exit`, then `{"check": "passed"}` | final run after the last edit; output was in `/tmp/ox-004-smoke.log` |
| `make e2e` | not run | No behaviour of `crates/ox` or the UI's TypeScript changed: neither file set is in the diff, and the app passes `WorkspaceInfo` through unchanged apart from carrying one more field the UI does not read yet. |

New tests in `crates/oxd/tests/worktrees.rs`:

- `branches_are_listed_most_recently_committed_first`: local `["newer",
  "main"]` (`newer` committed in 2090, so name order would differ), remote
  `origin/feature` and `origin/main` with `origin/HEAD` present in the clone
  but not listed, `fetch_error` `None`, the same from a subdirectory.
- `branches_are_refused_outside_a_working_tree`: a plain directory and a bare
  repository, with and without `fetch`, answer `INVALID_REQUEST`.
- `a_fetch_brings_in_new_remote_branches`: a branch added to the remote after
  the clone is absent without fetch and present with it.
- `a_failed_fetch_is_reported_beside_the_branches_as_they_stand`:
  `remote.origin.url` at a missing path; `fetch_error` names it; lists equal
  the unfetched ones.
- `requests_for_one_repository_share_its_fetch`: the uploadpack script
  counts fetches; two requests, both succeed, one fetch reaches the remote.
- `a_fetch_runs_with_the_clients_environment`: the client's
  `GIT_CONFIG_GLOBAL` redirects the remote with `insteadOf` and the fetch
  fails; without it the fetch succeeds.
- `workspaces_say_whether_they_are_in_a_git_working_tree`: a repository and
  its subdirectory `true`; `.git` and a plain directory `false`; after `git
  init` in the plain directory, a client connecting brings a watch listing
  with `true`.

Undo checks (each change made, the test run and seen to fail, the change
reverted):

| Change | Failing test | Failure |
| --- | --- | --- |
| A fresh map key per request (no coalescing) | `requests_for_one_repository_share_its_fetch` | fetches `2`, expected `1` |
| No `--sort=-committerdate` | `branches_are_listed_most_recently_committed_first` | local `["main", "newer"]` |
| Symbolic refs kept | `branches_are_listed_most_recently_committed_first` | remote includes `origin/HEAD` |
| Fetch with an empty client env | `a_fetch_runs_with_the_clients_environment` | "the fetch failed" expectation panicked: no `fetch_error` |
| No `.git`-component rule | `workspaces_say_whether_they_are_in_a_git_working_tree` | `.git` workspace flagged true |
| Flag cached per path instead of computed per listing | `workspaces_say_whether_they_are_in_a_git_working_tree` | no listing followed the connection (timeout) |
| Working-tree check only when fetching | `branches_are_refused_outside_a_working_tree` | bare repository answered `GIT` (8), not `INVALID_REQUEST` |

## Session provenance

| Stage | Role/name | Coordinator ID | Harness | Model/effort | Native conversation ID |
| --- | --- | --- | --- | --- | --- |
| parent | xtask coordinator | `2ea441ae` | Claude Code (Hypercube) | claude-opus-5-5 | `57fda6b3-c0d1-4d32-a25b-993c5391c607` |
| plan, revision 3 | planner | `planner` | Claude Code (Hypercube child) | opus / high | `5ce61adf-7d6c-4b85-ab78-5167733b9e23` |
| work, repair 1 | xwork worker | `impl-004` (child of Hypercube session `2ea441ae`) | Claude Code | Opus 5.5 (`claude-opus-5-5`), high | `4ec916a3-0230-44f5-8fc8-0d02c0f2aa1d` |
| review rounds 1–2 | review-004 | `review-004` | Claude Code (Hypercube child) | Opus 5.5, high | `578a67c9-ec46-4f62-b305-3cffeddf24dc` |

## Limitations

- **The 60-second timeout and its `SIGTERM` to the process group are not
  tested.** A 60-second test is too slow, and AGENTS.md rules out a
  test-only timeout flag (as the plan says).
- **A fetch running when `oxd` exits is not stopped.** It runs in its own
  process group, so a terminal's signal does not reach it, and nothing kills
  it on shutdown or idle exit; it finishes, or hangs, on its own. The plan
  does not cover shutdown.
- **A fetch task that panicked would leave its map entry**, and every later
  fetch of that repository would answer "the fetch stopped without an
  outcome" until `oxd` restarts. The task has no code that panics short of a
  poisoned mutex.
- **The coalescing test assumes an ordering** it does not enforce (see
  Deviations).
- Branch names are decoded from git's output as UTF-8, lossily, since the
  protocol carries strings.

## Repair round 1

- Plan: `.agents/plans/2026-11-12-004-workspace-branches.md` revision 3, blob
  `79e61a66b9c32427e90bd4f2fc756a538658a90a` (003's plan carries the matching
  revision 4 note). Review: `.agents/reviews/2026-11-12-004-workspace-branches.md`.
- Findings addressed: the plan finding the user decided. The "not in a Git
  working tree" refusal now carries git's reason. `worktree::not_a_working_tree`
  takes git's trimmed stderr and answers `INVALID_REQUEST` "`<path>` is not in
  a Git working tree: <git's message>", or the bare message when there is none.
  `worktree::plan` (committed 003 code) and `branches::common_dir` pass the
  `GitError`'s stderr through a new `GitError::stderr()`; their second refusals
  (no top-level name, empty common-dir output) keep the bare message. 003's
  assertions use `contains` on the old message and pass unchanged.
- New test `a_git_directory_git_rejects_is_refused_with_gits_reason`: a
  workspace whose `.git` file reads `gitdir: <tempdir>/missing` lists
  `git_working_tree: true`; `session.new` with a worktree and
  `workspace.branches` both answer `INVALID_REQUEST` containing "is not in a
  Git working tree: fatal: gitfile does not point to a valid repository".
- Undo check: dropping the stderr in `worktree::plan` fails the test with the
  bare message from `session.new`; dropping it in `branches::common_dir` fails
  it with the bare message from `workspace.branches`. Both reverted.
- Docs: `docs/architecture.md` says both requests refuse a workspace git finds
  in no working tree, including a rejected `.git`, with git's reason.
- Changed paths: `crates/oxd/src/worktree.rs`, `crates/oxd/src/branches.rs`,
  `crates/oxd/tests/worktrees.rs`, `docs/architecture.md`, this log.

| Command | Result | Environment | Evidence |
| --- | --- | --- | --- |
| `make check` | passed: fmt, clippy `-D warnings`, all workspace tests (`worktrees` 26 passed, `daemon` 47, store/state 44, `ox-protocol` 11, others), Biome, `tsc`, ruff | as above | final run after the last edit, native conversation |
| `make smoke-claude` | passed (`{"check": "passed"}`) | as above | final run after the last edit, native conversation |
| `make e2e` | not run | — | `crates/ox` and the UI's TypeScript are still unchanged. |

Dubious ownership, the plan's other example, is not tested: producing it
needs a repository owned by another user, which needs root.

## Parent verification

- Verifier: parent coordinator, Hypercube session 2ea441ae, native
  conversation `57fda6b3-c0d1-4d32-a25b-993c5391c607`.
- Review round 1 accepted the code with one plan finding: the "not in a Git
  working tree" refusal dropped git's reason, contrary to the confirmed
  behaviour. The user decided to append git's message; the planner revised
  this plan (revision 3) and 003's (revision 4 note), and repair round 1
  implemented it. I read the fix: `not_a_working_tree` takes git's trimmed
  stderr in `worktree::plan` and `branches::common_dir`, and
  `a_git_directory_git_rejects_is_refused_with_gits_reason` covers both
  requests, failing with either half undone.
- The fix changed a contract, so review-004 re-reviewed it (round 2): accept,
  no blocking or plan findings.
- Checks: `make check` and `make smoke-claude` passed after the last code
  edit; `make e2e` not needed (`crates/ox` and the UI's TypeScript unchanged).
- Notes from review, not fixed: the coalescing test relies on an ordering it
  does not enforce (260 of 260 runs passed, including under full CPU load); a
  fetch running when `oxd` exits is not stopped; a panicked fetch task would
  leave its map entry; a fetch failing with empty stderr reports an empty
  `fetch_error`; the 60-second timeout is untested; a plain directory's
  refusal repeats git's "not a git repository"; a git that cannot start is
  reported under the "not in a Git working tree" framing; dubious ownership is
  untested (needs root).
