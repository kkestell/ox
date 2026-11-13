# Git workflows

Ox's UI supports a few opinionated git workflows, the ones its user works in,
rather than a general tool for every git workflow. Each workflow is a
composition of generic primitives. No workflow is built into the low levels of
the code. GitHub support stays small: `oxd` reaches GitHub through the `gh`
CLI.

## Workflows

**Local merge.** For personal projects that live on `main`.
- Each task is a session in its own worktree, branching from the workspace's
  `HEAD`.
- When the task is done, the agent rebases its branch onto `main` and squashes
  it to one commit. Ox then fast-forwards `main` to it in the workspace's
  checkout.
- Deleting the session removes the worktree and the branch, which is now
  merged.
- The user pushes `main` when they choose.

**Pull request.** For work repositories on GitHub.
- Each task is a session in its own worktree, on a named branch that starts at
  a freshly fetched remote default branch, such as `origin/main`.
- Several tasks can run at once.
- The agent pushes the branch and opens the PR. Review, CI and merging happen
  on GitHub.
- Once the PR is merged, deleting the session removes the worktree and deletes
  the branch, including after a squash merge.

**Review.** For pull requests the user is asked to review.
- A list shows the user's PRs and the PRs awaiting their review.
- Reviewing a PR does the following:
  - finds the workspace for the PR's repository, cloning the repository if it
    isn't on disk;
  - fetches the PR's head;
  - starts a session in a worktree on an Ox-owned branch at that head.
- The agent reviews the PR and posts comments, an approval or a request for
  changes.
- When the author pushes again, the next round moves the review branch to the
  new head and continues the same session, so the agent keeps its context.

Local merge is built first, because it needs no GitHub. Pull request and
review follow.

## Layers

| Layer | Holds |
|---|---|
| `oxd` | Workspaces, sessions, worktrees, branches, repositories, and GitHub state. Every operation is generic. |
| Clients (the app and `oxctl`) | Presentation, and the workflows: which operations to call, with which parameters, in which order. |

Every workflow can be run and tested from `oxctl` before it has a UI.

## What belongs in `oxd`

See [What belongs in oxd](../../docs/architecture.md#what-belongs-in-oxd).

## Primitives

### Existing

- **Workspace**: a directory.
- **Session**, in one of:
  - the workspace's checkout;
  - a new worktree on an auto-named branch;
  - a new worktree on an existing branch;
  - a new worktree on a new branch at a start point.
- **Branch listing**, with an optional fetch of every remote, and the worktree
  that has each local branch checked out. Concurrent fetches of one
  repository share a single run.
- **Delete and archive**, with the unsaved-work check and the merged-branch
  rule.

### New

**Compare.**
- A worktree session has a comparison ref, such as `main` or `origin/main`.
  The client sets it when it creates the session, and can change it later.
- `oxd` publishes the comparison with the session, to every watching client:
  - the branch's tip, and the commit the ref points at;
  - commits ahead of the ref and commits behind it.
- `oxd` recomputes the comparison:
  - when the session's agent finishes a turn;
  - after any move of a branch, delete or fetch in the repository, for every
    session of that repository;
  - when a client asks for a refresh.
- Commits made outside Ox appear at the next of these events.

**Move a branch.**
- Sets a branch to a commit, either fast-forward only or forced.
- The caller passes the commit it expects the branch to be at. The move fails
  if the branch has moved since.
- When the branch is checked out in a worktree or in the workspace's checkout,
  the move updates that checkout and its index along with the branch. It
  refuses:
  - uncommitted changes to tracked files there;
  - an untracked file the move would overwrite;
  - a session's agent in that checkout that is starting, in a turn, or
    waiting for the user's answer.

  Untracked files the move leaves alone, and an idle agent, don't stop it.
- Local merge uses it to fast-forward `main`. Review uses it to reset the
  review branch to the PR's new head.

**Delete with a branch choice.**
- The caller says whether the session's branch is deleted, kept, or decided by
  the merged-branch rule. The rule is the default.
- Deleting the branch is separate from discarding unsaved work in the
  worktree.

**Fetch refspecs.**
- Branch listing's fetch and session creation can name refspecs to fetch,
  such as `refs/pull/<n>/head`. Without refspecs, every remote is fetched.
- These fetches use the same machinery as the existing one: the client's
  environment, a timeout, cleanup of git's processes, and one shared run per
  repository.
- Session creation fetches before it resolves its start point. A failed fetch
  creates nothing.

**Clone.**
- Clones a URL into a directory the client names, then adds it as a workspace.
- Concurrent clones of one URL share a single run.
- A clone that fails or is interrupted leaves no directory and no workspace.

**Workspace settings.**
- Key–value pairs that `oxd` stores on a workspace for its clients, without
  interpreting them.
- Clients store the workspace's default workflow there.

**GitHub.**
- `oxd` reaches GitHub through the `gh` CLI, with the client's environment, so
  it uses the user's sign-in.
- Each workspace reports the GitHub repository its remotes point to.
- `oxd` keeps the user's open PRs and the PRs awaiting their review. It
  publishes them to watching clients and refreshes them on request and at an
  interval while a client watches.
- A session can be linked to a PR, either by number or as the PR whose head is
  the session's branch. The pull request workflow uses the second, after the
  agent opens the PR. `oxd` keeps the linked PR's state (open,
  merged or closed, its head commit, and its checks) and publishes it with the
  session.
- A review's next round finds its session by the PR link.

## Composition

Each workflow step is one `oxd` operation, or a short sequence where every
prefix leaves a valid state:

- **Starting a review:** clone the repository if no workspace has it, then
  create the session, fetching the PR's head as its start point and linking
  the PR. If it stops after the clone, the user just has the workspace.
- **A review's next round:** fetch the PR's head, then move the review branch
  to it. If it stops after the fetch, nothing changes for the session.

## Left to the agent

Rebasing, squashing, resolving conflicts, pushing, opening a PR, and posting a
review are git and `gh` work that the agent does in its worktree.

## Workflow choices

These are parameters that a workflow passes to the primitives:

- **Start point**: `HEAD` for local merge; the fetched remote default branch
  for pull requests; the fetched PR head for reviews.
- **Comparison ref**:
  - local merge: the workspace's branch;
  - pull request: the remote default branch.
- **Branch name**:
  - local merge: the auto-named `ox/<id8>`;
  - pull request: a name the user gives;
  - review: an Ox-owned branch per PR.
- **Default workflow** for New Task: a workspace setting. It doesn't stop any
  other workflow from running in that workspace.
- **Matching a PR to a workspace**: by the GitHub repository each workspace
  reports. When more than one workspace matches, the user chooses.
- **Deleting a PR's branch**: once the linked PR is merged, Delete deletes the
  branch, including after a squash merge.

## UI

### Starting a session

- A workspace's **+** button starts a **New Task** in the workspace's default
  workflow.
  - **Local merge:** one click. A new worktree from `HEAD` on an auto-named
    branch.
  - **Pull request:** New Task first asks for the branch name.
- The workspace's menu keeps the agent picker and **Session in Checkout**,
  which runs a session in the workspace's own directory. That is the only
  choice for a workspace outside Git.
- A workspace's default workflow is set from its context menu: local merge or
  pull request. Local merge is the default.
- Choosing an existing branch or a new branch at a start point stays available
  in `oxctl`. The UI doesn't offer it.

### Session status

- A worktree session's row shows its git status as an icon at the row's right
  end. The row actions take the same place on hover, fading in over the icon.
- The icon's tooltip gives the detail.
- Status colours follow the app's design tokens.

**Local merge**, from the session's comparison:

| State | Icon | Tooltip |
|---|---|---|
| No commits | None | "No commits ahead of main" |
| One commit, not behind | `GitBranch` | "1 commit ahead of main" |
| Behind, or more than one commit | `GitBranch`, attention colour | "3 commits ahead, 2 behind main" |

**Pull request:** `GitPullRequest`, coloured by the linked PR's state, with
its number, state and checks in the tooltip.

### Finishing

- The conversation's header has the session's **Finish** button.
- For local merge it reads **Merge into main**.
  - It is enabled only when the branch is one commit ahead of the workspace's
    branch and not behind it.
  - Otherwise it is disabled, and its tooltip gives the reason: "No commits",
    "Behind main: ask the agent to rebase", or "2 commits: ask the agent to
    squash".
  - Clicking it fast-forwards the workspace's branch with Move a branch, then
    offers to delete the session.
  - When the move is refused, for example by uncommitted changes in the
    workspace's checkout or a session's agent working there, Ox shows the
    reason and nothing changes.
- For pull requests it reads **Open Pull Request**, and asks the agent to push
  and open the PR. Once `gh` reports the PR merged, the session offers Delete,
  which deletes the branch.

### Pull requests panel

- The right panel lists the user's PRs and the PRs awaiting their review.
- Each PR awaiting review has a **Review** button.
  - When no session is linked to the PR, it starts a review session.
  - Otherwise it moves that session's review branch to the PR's new head and
    opens the session.

## Constraints

Resuming a session needs Claude's own transcript, which Claude deletes after
`cleanupPeriodDays` (30 days by default). After that, a later review round can
show the old conversation but can't continue it.
