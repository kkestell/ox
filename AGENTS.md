# Experimental Claude Code multiplexer

Optimize for learning and code that is cheap to change.

## Status

We test with `make dev`. Ox's test state is disposable.

## Source Map

```
.
├── AGENTS.md                  # agent instructions
├── agents                     # workflow records
│   ├── audits                 # audits from /ox-audit and others
│   ├── plans                  # plans from /ox-plan
│   ├── reviews                # reviews from /ox-review
│   └── scratch                # research notes
├── .githooks
│   └── pre-commit             # records metrics, checks docs formatting
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
│       └── main.go            # daemon startup
├── docs
│   ├── architecture.md        # how oxd and ox are built
│   ├── functional-requirements.md # what users see and rely on
│   ├── harnesses
│   │   └── claude-code.md     # observed Claude Code behavior
│   ├── known-issues.md        # unfixed defects
│   ├── metrics.md             # size and complexity, generated
│   ├── protocol.md            # the interface oxd serves
│   └── todo.md                # planned work
├── dprint.json                # Markdown formatting for docs
├── go.mod                     # Go module
├── go.sum                     # dependency checksums
├── internal
│   ├── agent
│   │   ├── agent.go           # harness interface
│   │   ├── tracker_test.go    # tracker tests
│   │   └── tracker.go         # untaken inputs and published state
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
│   │   └── fake.go            # echo harness
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
│   │   ├── live_test.go       # live Claude and Ur tests
│   │   ├── mcp.go             # MCP connections
│   │   ├── methods_test.go    # method tests
│   │   ├── methods.go         # RPC methods
│   │   ├── server.go          # listener, workspaces, and sessions
│   │   └── session.go         # session goroutine and lifecycle
│   ├── store
│   │   └── store.go           # SQLite persistence
│   └── ur
│       ├── stream.go          # OpenRouter requests and streams
│       ├── ur_test.go         # Ur tests
│       └── ur.go              # Ur agent
├── Makefile                   # build, test, run, and format docs
└── scripts
    └── metrics.py             # writes docs/metrics.md
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
  process and fire timers, and deliver what they produce through `Events`.
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
  become one place that the other reads.
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
  - **Record** in `docs/known-issues.md`: a defect that cannot be fixed yet,
    because it needs reproducing or depends on Claude Code behavior Ox cannot
    change. The entry states the defect's worst cost, how it was found, and
    what it waits on.
- A review reads `docs/known-issues.md` first and reports a recorded defect
  again only when what it waits on has changed.
- Remove an entry once its defect is fixed or shown not to exist.

## Workflow

The user settles the direction and rough scope of a change with a top-level
agent, which carries it through child sessions. When Codex leads the workflow,
use Codex child sessions on Sol 6.2. The Claude model choices below apply when
Claude Code leads the workflow.

The checkout holds one change at a time, and no code change is committed before
its review is Ready to commit. Before starting a change, the top-level agent runs
`git status` and carries any uncommitted work through the steps below to a
commit, or asks the user about work it cannot place. Worktrees for parallel
changes are cut only from a clean checkout. A child session that finds
uncommitted work outside its brief stops and reports it to the session that
started it.

1. A child session on Opus 5.5 at high effort runs `/ox-plan`. The user
   approves the plan's requirement changes with its other product questions.
2. The top-level agent sanity-checks the plan with a quick look at the code it
   names, without redoing the planning.
3. A child session on Sonnet or Opus, chosen by the change's complexity,
   runs `/ox-work` on the plan.
4. A child session on Opus or Sol runs `/ox-review` on the uncommitted
   changes for the plan.
5. The implementing session runs `/ox-work fix` on the review.
6. The reviewing session runs `/ox-review verify` on its review. Open
   findings or new regressions go back to the implementing session, until
   the verdict is Ready to commit.
7. The top-level agent sanity-checks the result, including the requirements
   diff, and commits the change with its plan and review in `agents/`.

The user owns `docs/functional-requirements.md`. It changes only when a change
adds, alters, or removes behavior the user can observe, and only with the text
the user approved in the plan. A bug fix leaves it alone unless the fix shows
a requirement was wrong. No agent rewords a requirement to match the code.
Plans and reviews cite requirements by title. A periodic `/ox-review` of the
whole repository checks the code against every requirement.

A periodic `/ox-audit` finds the structures that let classes of defects
happen, drawing on past plans, reviews, and fixes as evidence. Each finding
the user chooses to change goes through the workflow above, with the finding
as the request for `/ox-plan`.

Every audit finding marked to plan or to fix, from `/ox-audit` or any other
audit, goes into `docs/todo.md` when the audit is written: one entry that
names the defect and links its section in the audit. The change that fixes
it removes the entry in the same commit. An audit or review that finds an
entry stale corrects or removes it.

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
- A change is done once every check that covers it has run: `make test` and
  `make e2e`; `make live` when it changes how Ox drives Claude Code; and a
  check in headless Chrome through the `make dev` stack when it changes how
  the page reaches `oxd`. A check is reported as not run only when something
  the agent cannot change blocks it, named in the report.

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
  and rely on, each one a user would call a bug if it broke.
- `docs/known-issues.md`: defects that cannot be fixed yet, kept as the
  Defects section describes.
- `docs/todo.md`: planned work, by priority, including every unfixed audit
  finding marked to plan or to fix.
- `docs/metrics.md`: line counts and cognitive complexity, written by
  `make metrics` and the pre-commit hook. Nobody edits it by hand.

Every fact lives in one place. A document that needs a fact owned by another
links to it instead of restating it. Most changes need no doc edits: edit a
document only when the change alters a fact it owns. Correct or delete a wrong
passage without expanding it. Add a document or a section only when asked.
`make docs` formats the documents in `docs/` with dprint, and the pre-commit
hook refuses a commit whose documents are unformatted.
