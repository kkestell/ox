# Ox

Ox runs coding agent sessions, in Claude Code and Ur, side by side in a web
UI.

Optimize for learning and code that is cheap to change.

## Status

We test with `make dev`. Ox's test state is disposable.

## Source Map

```
.
├── AGENTS.md                  # agent instructions
├── agents                     # workflow records
│   ├── audits                 # audits from /ox-audit, /ox-sweep, and others
│   ├── plans                  # plans from /ox-plan
│   ├── reviews                # reviews from /ox-review
│   └── scratch                # research notes
├── .githooks
│   └── pre-commit             # checks docs formatting
├── .github
│   └── workflows
│       └── ci.yml             # PR checks
├── .golangci.yml              # Go linters
├── bin                        # build output
│   ├── ox
│   ├── ox-mcp
│   └── oxd
├── cmd
│   ├── ox                     # web UI server
│   │   ├── main_test.go       # relay tests
│   │   ├── main.go            # web UI and its relay to oxd
│   │   └── web                # web client
│   ├── ox-mcp                 # MCP forwarder
│   │   ├── main_test.go       # forwarder tests
│   │   └── main.go            # stdio forwarder to oxd
│   └── oxd                    # daemon
│       ├── main_test.go       # startup tests
│       ├── main.go            # daemon startup
│       ├── noscene.go         # no scene player in normal builds
│       └── scene.go           # -scene flag in screenshots builds
├── docs
│   ├── architecture.md        # how oxd and ox are built
│   ├── functional-requirements.md # what users see and rely on
│   ├── harnesses
│   │   └── claude-code.md     # observed Claude Code behavior
│   ├── images                 # README screenshots
│   ├── protocol.md            # the interface oxd serves
│   └── screenshots            # README screenshot scene and fixture repos
├── dprint.json                # Markdown formatting for docs
├── go.mod                     # Go module
├── go.sum                     # dependency checksums
├── internal
│   ├── agent
│   │   ├── agent.go           # harness interface
│   │   ├── tracker_test.go    # tracker tests
│   │   └── tracker.go         # untaken inputs and published state
│   ├── children
│   │   └── children.go        # processes oxd waits for and kills
│   ├── claude
│   │   ├── agent.go           # Claude agent state machine
│   │   ├── approval_test.go   # approval tests
│   │   ├── approval.go        # tool approvals
│   │   ├── claude_test.go     # startup tests
│   │   ├── claude.go          # Claude process and input
│   │   ├── configure_test.go  # configuration tests
│   │   ├── configure.go       # setting changes on a running process
│   │   ├── messages.go        # Claude output format
│   │   ├── park_test.go       # parking tests
│   │   ├── park.go            # when the process can be parked
│   │   ├── prompt_test.go     # prompt tests
│   │   └── prompt.go          # prompt lifecycle
│   ├── config
│   │   ├── config_test.go     # configuration tests
│   │   ├── config.go          # user configuration
│   │   └── default.json       # built-in configuration
│   ├── fake
│   │   ├── fake.go            # echo harness
│   │   ├── scene_test.go      # scene tests
│   │   └── scene.go           # scripted harness that plays a scene
│   ├── jsonrpc
│   │   └── jsonrpc.go         # JSON-RPC messages
│   ├── paths
│   │   └── paths.go           # XDG file locations
│   ├── server
│   │   ├── client_test.go     # test client
│   │   ├── conn.go            # client connections
│   │   ├── files.go           # workspace folder listing and file reads
│   │   ├── history_test.go    # history tests
│   │   ├── history.go         # session history
│   │   ├── id_test.go         # ID tests
│   │   ├── id.go              # Ox's IDs
│   │   ├── live_test.go       # live Claude and Ur tests
│   │   ├── mcp_test.go        # MCP tests
│   │   ├── mcp.go             # MCP connections
│   │   ├── methods_test.go    # method tests
│   │   ├── methods.go         # RPC methods
│   │   ├── server.go          # listener, workspaces, and sessions
│   │   ├── session.go         # session goroutine and lifecycle
│   │   ├── worktree_test.go   # worktree tests
│   │   └── worktree.go        # session worktrees
│   ├── store
│   │   ├── store_test.go      # store tests
│   │   └── store.go           # SQLite persistence
│   └── ur
│       ├── instructions.go    # system message
│       ├── shell_test.go      # shell tool tests
│       ├── shell.go           # shell tool
│       ├── stream.go          # OpenRouter requests and streams
│       ├── ur_test.go         # Ur tests
│       └── ur.go              # Ur agent
├── Makefile                   # build, test, run, and format docs
└── scripts
    ├── metrics.py             # prints size and complexity
    └── prune.sh               # removes merged branches and their worktrees
```

## Implementation

- Keep the code direct, readable, and simple. Implement the requested behavior
  completely, using the simplest coherent design, judged by the code that
  results rather than by the size of the diff.
- Handle normal use and concrete failures. Do not build machinery for unusual
  edge cases or hypothetical failures. Fail clearly when something unsupported
  happens.
- Avoid speculative abstractions, compatibility layers, automatic recovery,
  and durability guarantees. Add them only when a current requirement earns
  their cost.
- Keep state and lifecycle ownership easy to follow. Reconsider the design when
  a change needs coordination machinery to support earlier choices.
- Treat research and prior implementations as evidence, not requirements.
  Keep unresolved protocol choices open until they need a decision.
- A session's state is owned by its goroutine, and other code reaches it only
  by posting an operation to it. A harness's own goroutines only read its
  process, fire timers, and do the I/O its turn needs, such as Ur's HTTP
  requests and shell commands, and deliver what they produce through `Events`.
- A response is queued by the owner of the state it reports, when it reads
  that state.
- A request carries only what the user changed, and `oxd` applies it to the
  current state.
- A fact inferred from a harness's tool calls or hooks names the event that
  clears it, and has a live reproduction.
- `internal/agent` changes only for what a real harness needs. Harness
  payloads pass through the server untouched.
- Session updates have one type, `agent.Update`, sent to clients and recorded
  as history in that type.
- The body of a commit that adds a state, timer, retry, lock, or stored field
  names the user-visible behavior that needs it and the races and cleanup it
  brings.

## Correct by construction

Most of Ox's defects come from a few structures: a state the types allow but
the design forbids, one fact kept in two places, and a rule each caller
decides for itself. Build so those states cannot exist, and the defects that
need them cannot happen.

- Make illegal states unrepresentable. Fields that are valid only together,
  or only in one state, live in one value that exists only while they are
  valid, instead of beside a flag or as zero values every reader must check.
  In TypeScript, a state with its own fields is a discriminated union.
- Give each fact one source. Derive whatever can be derived from it instead of
  storing a copy and keeping the copy in sync. Two places that must agree
  become one place that the other reads. A request the page has sent and
  `oxd` has not answered is the page's own fact, not a copy of `oxd`'s state.
- Decide each rule in one place. When several callers handle the same case,
  such as a parked session, the case belongs to the state's owner, and the
  callers take its answer.
- Check input once, at the boundary, into a type that holds only valid values.
  Code past the boundary relies on the type.
- A design that rules out a wrong state is a simplification: it replaces the
  checks, guards, and recovery that would otherwise handle that state. Choose
  it over a smaller diff that leaves the state possible.

## Conventions

- Ox's files live in the XDG base directories, beneath an `ox` directory.
  `internal/paths` is the one place that resolves them. Whoever creates a file
  creates its directory.
- A setting or field has one name everywhere: configuration, protocol, store,
  Go, and the web client all use the protocol's name, such as
  `permission_mode`.
- `oxd` checks every request against the configuration it serves, the way it
  checks the configuration's defaults.
- Error messages from `oxd` follow Go style: lowercase, no trailing period.
  The standard JSON-RPC messages keep the specification's text.
- A log line about a session names it by its ID.
- The web client shows a request's error with `createAttempt` and asks for
  confirmation with a `<dialog>`.

## Defects

- Fix every confirmed defect. A defect is confirmed live, from the source, or
  by an injected fault.
- Fix a defect at its cause. A race that can leave state the design says
  cannot exist comes from who owns the state, however unlikely it is. Fix it
  by changing who owns the state, not by handling the interleaving. A race
  between independent requests in which every outcome is valid is not a
  defect.
- A review gives each finding one of three outcomes:
  - **Fix:** a confirmed defect, or a design, test, or documentation problem
    whose fix costs less than keeping it.
  - **Reject,** with the reason in the review: the finding is speculative,
    misreads the intended behavior, or costs less to keep than to fix, such
    as an unmeasured scaling cost.
  - **Record** as a GitHub issue labeled `bug`: a defect that cannot be fixed
    yet, because it needs reproducing or depends on Claude Code behavior Ox
    cannot change. The issue states the defect's worst cost, how it was found,
    and what it waits on.
- A review reads the open `bug` issues first. It skips a defect that an open
  `bug` issue records, unless the change resolves that issue or what the issue
  waits on has changed, and checks that the change fixes each issue it
  resolves.
- Close an issue once its defect is fixed or shown not to exist.

## Workflow

The user settles the direction and rough scope of a change with a top-level
agent, which plans and implements the change itself and starts a child session
only to review it. It reads a child's final reply, which Ox delivers, and not
the child's transcript. When Codex leads the workflow, use Codex child sessions on
Sol 6.2. The Claude model choices below apply when Claude Code leads the
workflow.

The main checkout stays on `main` and only pulls. Each change runs in an Ox
worktree session started from the main checkout, and every step below runs in
its worktree. Before its first commit, the top-level agent renames the
worktree's branch for the change with `git branch -m <slug>`. A child session
that finds uncommitted work outside its brief, other than its sibling
children's, stops and reports it to the session that started it. Changes reach
`main` only by pull request, which the user merges.

Several changes run at once, each with its own top-level agent. Those agents
are peers: each decides for its own change, and they settle what their changes
share by talking with `send_message`. Before its plan starts, and again before
its push, a top-level agent checks the other worktrees (`git worktree list`)
and open pull requests for changes to the same code, protocol, requirements,
or agent instructions, and tells each one's top-level agent what its change
alters there. A change to `AGENTS.md`, a skill, or a requirement reaches other
worktrees only when they rebase, so its agent sends the new text to every
other top-level agent with a change in flight.

The steps scale with what the change can break. A small change adds no state,
timer, retry, lock, stored field, protocol message, or requirement change: a
style rule, copy, or a bug fix whose cause is known. The top-level agent makes
a small change itself in its worktree, with the test that fails without it,
runs the checks that observe it, and opens the pull request, with no plan or
review session. Coral reviews every pull request. Every other change takes the
steps below.

1. The top-level agent runs `/ox-plan` itself. The user approves the plan's
   requirement changes with its other product questions.
2. The top-level agent runs `/ox-work` on the plan itself.
3. A child session runs `/ox-review` on the uncommitted changes for the plan:
   on Sonnet, or on Opus when the change alters a state owner, concurrency, or
   how Ox drives Claude Code. The request passes on `/ox-work`'s final
   response.
4. The top-level agent runs `/ox-work fix` on the review. When the review has
   a High or Medium `Fix` finding, the reviewing session runs
   `/ox-review verify` on its review, and open findings or new regressions go
   back to the top-level agent until the verdict is Ready to commit.
5. The top-level agent checks the requirements diff and commits the change
   with its plan and review in `agents/`. It runs `git fetch` and
   `git rebase origin/main`, then re-runs the checks that observe the change
   when the rebase brought in new commits. It pushes with
   `git push -u origin <slug>` and opens the pull request with
   `gh pr create`, written as [Pull requests](#pull-requests) says. The
   top-level agent then tells the user the pull request is ready to merge.
6. A pull request merges by squash, with its title as the commit subject.
   After the user merges, the top-level agent pulls and runs `make prune` in
   the main checkout. `make prune` removes each local branch whose pull
   request has merged, and its worktree.

The user owns `docs/functional-requirements.md`. It changes only when a change
adds, alters, or removes behavior the user can observe, and only with the text
the user approved in the plan. A bug fix leaves it alone unless the fix shows
a requirement was wrong. No agent rewords a requirement to match the code.
Plans and reviews cite requirements by title. A periodic `/ox-sweep` checks
the whole repository against every requirement.

A periodic `/ox-audit` finds the structures that let classes of defects
happen, drawing on past plans, reviews, and fixes as evidence. Each finding
the user chooses to change goes through the workflow above, with the finding
as the request for `/ox-plan`.

Every audit finding marked to plan, from `/ox-audit` or any other audit,
becomes a GitHub issue labeled `bug` that links its section in the audit.
The audit lands through its own pull request. The pull request that fixes a
finding closes its issue. An audit or review that finds an issue stale
corrects or closes it.

## Pull requests

A pull request explains its change to someone who uses Ox but has not read the
code, the issue, or the plan. It reads like a short note from one engineer to
another, in plain words.

The title is the commit subject: what the change does, as a user would say it,
such as "Show a file that became a symlink in the Changes tab".

The body takes this form and no other sections:

```markdown
One short paragraph with the big idea: what was wrong or missing, and what the
user sees now.

## Changes

- One bullet for each change in behavior.

## Testing

One or two sentences on what the tests cover and what was checked by hand.

## Screenshots

| View | Before | After |
| ---- | ------ | ----- |

Fixes #N
```

- Explain each concept the reader needs in a few plain words the first time it
  appears, as for someone new to it. Describe behavior rather than functions,
  flags, or error text, unless the reader needs one to recognize the change.
- Testing names a check that could observe the change only when it did not
  run, with what blocked it.
- `## Screenshots` appears when the change alters the UI: a table of each view
  it changes, taken in headless Chrome, before and after from the base and the
  change for a view that exists on the base, and after alone for a view the
  change adds. The images are committed under `pr-<N>/` on the orphan
  `pr-assets` branch and linked through `raw.githubusercontent.com` at that
  commit.
- `Fixes #N` closes each issue the change resolves.
- The workflow's records stay in `agents/` and in the sessions: plans,
  reviews, verdicts, whether a change counted as small, and checks skipped
  because they cannot observe the change.

## Verification

- Each behavior a change adds or fixes keeps a test that fails without it, at
  the cheapest layer that observes it. A probe that confirms a behavior becomes
  that test. Tests check behavior through public interfaces, not the
  implementation.
- Prefer small live tests against real Claude Code processes, using Haiku only.
  Check observable protocol behavior rather than exact model wording. They live
  in `TestLive` and the web client's `approval.spec.ts`, and run with
  `make live`. Ur's live test, `TestLiveUr`, uses the built-in DeepSeek model
  through OpenRouter with `OPENROUTER_API_KEY` from `.env`.
- Keep test setup simple; avoid elaborate mocks and fake runtimes.
- `make test`, `make e2e`, and `make dev` build with the race detector. A data race fails
  the test, or stops `oxd` with the race's report.
- A change is done once the checks that can observe it have run: `make test`
  for Go and the Node suites; `make e2e` when it changes what the page does;
  `make live` when it changes how Ox drives Claude Code; a check in headless
  Chrome through `make dev` in the change's worktree when it changes how the
  page reaches `oxd`; and the user's phone for behavior only a phone shows. A
  check that cannot observe the change is skipped. A check that can observe it
  is reported as not run only when something the agent cannot change blocks
  it, named in the report.

## Documentation

The code describes the implementation, and Git history records changes.
Comments explain non-obvious reasons or external rules. Each document owns the
facts listed for it:

- `AGENTS.md`: instructions for agents working anywhere in the repository.
- `cmd/ox/web/AGENTS.md`: instructions for agents working on the web client,
  and the Solid 2.0 reference.
- `docs/architecture.md`: how `oxd` and `ox` are built: their components, the
  boundaries between them, and what each owns.
- `docs/protocol.md`: the interface `oxd` serves: messages, methods, updates,
  events, and errors.
- `docs/harnesses/claude-code.md`: how Claude Code behaves when driven with `claude -p`
  and stream-json, as observed.
- `docs/functional-requirements.md`: the behavior the person using Ox can see
  and rely on, each one a user would call a bug if it broke. A requirement
  states it in the words the user would use to report it broken: outcomes, the
  limits they hit, the data that survives, what is refused and why, the order
  they depend on, and the boundaries Ox keeps. It leaves to the code and tests
  the values a designer or maintainer could change without the user calling it
  a regression, such as sizes, breakpoints, colors, placement, catalogs,
  protocol names, and internal states.

Planned work and defects live in the repository's GitHub issues, labeled
`enhancement` and `bug`.

Every fact lives in one place. A document that needs a fact owned by another
links to it instead of restating it. Most changes need no doc edits: edit a
document only when the change alters a fact it owns. Correct or delete a wrong
passage without expanding it. Add a document or a section only when asked.
`make docs` formats the documents in `docs/` with dprint, and the pre-commit
hook refuses a commit whose documents are unformatted.
