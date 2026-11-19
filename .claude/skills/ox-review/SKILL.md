---
name: ox-review
description: Review Ox code in uncommitted changes, a commit, a branch, named files, or the whole repository, against its plan, Ox's guarantees, and `AGENTS.md`. Fix every confirmed finding, asking the user only for product decisions; reject the rest with a reason, or record in `docs/known-issues.md` what cannot be fixed yet. Write the review in `.agents/reviews/` and stage the fixes. Do not use for plan critiques or standalone documentation reviews. Never use it unless the user invokes it or explicitly asks for it.
argument-hint: "[review scope, and the plan it implements]"
---

Use this skill only when the user invokes it or explicitly asks for it. Never use it on your own initiative.

## Objective

Review the code the user names and leave every finding with one outcome, as `AGENTS.md` ("Defects") defines them: **Fixed**, **Rejected** with a reason, or **Recorded** in `docs/known-issues.md`. Decide every fix yourself except one that raises a product question, which the user decides.

Correctness matters for what happens in normal use and for the promises in `docs/guarantees.md`. Handling every case that could occur does not.

## Choose what to review

1. Read `<review_scope> $ARGUMENTS </review_scope>`. Ask for clarification only when the answer would change what you review.
2. For uncommitted changes, a commit, or a branch, review the changes and the code that calls or depends on them. For named files or the whole repository, review that full scope.
3. Read `AGENTS.md`, `docs/guarantees.md`, and `docs/known-issues.md`; for web client code, also `cmd/ox/web/AGENTS.md`. Read the sections of `docs/architecture.md`, `docs/protocol.md`, and `docs/claude-code.md` that cover the code under review. If the scope names a plan in `.agents/plans/`, read it: its Goal, its Decisions, and its settled Check your assumptions entries are the requirements.

## Review

4. Trace behavior through callers, state owners, resource lifetimes, and tests. Judge implementation choices by their purpose and consequence. Do not treat a language feature or coding pattern as a defect on its own.
5. Check the code for each of these categories the scope reaches:
   - **Intent.** The change delivers the plan's Goal, keeps its Decisions and the user's answers, and stays within it.
   - **Guarantees.** Each guarantee the code touches holds through the whole lifecycle of a page's request: in flight, its reply lost, a reconnect, navigation away and back, a second page acting on the same session, the process exiting or parking, and `oxd` running a page's independent requests in any order. Text the user typed is never lost or sent twice. The page shows an action as possible, or a value as applied, only once `oxd` has confirmed it.
   - **Settings.** The Claude process runs with the model, effort, permission mode, and account the page shows, including after a partial failure, a restart, or with an inherited environment. A setting Claude would silently ignore or default is refused.
   - **Settling.** Each wait on a reply, a pipe, a process, or a child of Claude ends, by what it waits on or by a bound. No path leaves a request, a stop, or a shutdown without a result, and each error is shown where its request was made.
   - **Process I/O.** Claude's output is read to its last line, including one that ends at EOF; Claude's exit is seen even while a child holds its pipes; a failure reason on stdout or stderr reaches the request that failed.
   - **Ownership.** State is changed only by its owner: the session goroutine in `oxd`, the session's control on the page. Each new state, timer, retry, lock, or stored field is needed by a user-visible behavior, and its races and cleanup are accounted for. A race that can leave state the design says cannot exist is a defect of ownership, however unlikely.
   - **Claude Code.** Each assumption about Claude's output, ordering, timing, or flags matches `docs/claude-code.md` and handles every variant it records, or has a live check. A fact inferred from Claude's tool calls or hooks names the event that clears it. Updates are attributed to the turn that produced them.
   - **Security.** Listeners, the relay, file paths, process environments, and files outside Ox's own directories admit only the user and touch only what Ox owns.
   - **Correctness.** Other required behavior: results, state changes, and error reporting.
   - **Testing.** Each test fails when the behavior it names breaks. Tests use the public interface of the code they test, and a live or e2e check waits for the event it claims to test, not an earlier one. A server path the fake harness does not reach has a live case.
   - **Simplicity.** Code that adds no required behavior, including speculative fallbacks, retries, and recovery for failures that have not happened.
   - **Conventions.** The rules in `AGENTS.md` and `cmd/ox/web/AGENTS.md`, such as one name per field and log lines that name the session.
   - **Documentation.** Each changed fact is stated by the document `AGENTS.md` assigns it, and no document contradicts the code.
6. Confirm each suspected finding by tracing the code, reproducing it, or running a focused check: a stand-in `claude` script, an injected `/rpc` delay or failure in an e2e spec, or a probe against built binaries on a temporary socket, database, and port. Report missing handling only when a guarantee or a reachable failure needs it. Do not ask for tests of hypothetical cases. Say when you reviewed only part of the scope. Do not change the code under review until Fix, and revert any edit a check needs before you finish.
7. Skip a defect `docs/known-issues.md` records unless what its entry waits on has changed.
8. Run each test the change adds against the code before the change, in a detached worktree of the base revision, and remove the worktree afterwards. Then remove each condition the change adds, one at a time, and confirm a test fails. A test that passes on the base revision, or that no removed condition breaks, is a `Testing` finding.

## Decide each outcome

9. Give each finding one outcome:
   - **Fix** a confirmed defect, or a design, test, or documentation problem whose fix costs less than keeping it.
   - **Reject** a finding that is speculative, misreads the intended behavior, or costs less to keep than to fix, such as an unmeasured scaling cost. State the reason.
   - **Record** a confirmed defect that cannot be fixed yet, because it needs reproducing or depends on Claude Code behavior Ox cannot change.
10. A fix needs a decision only when it raises a product question, a choice that changes what the user sees or does in everyday use, that the plan and the user's answers do not settle. Design, interface, and internal behavior choices are yours, and so is how a rare case behaves. Severity, size, and file count alone do not make a fix need a decision.

## Fix

11. Fix each finding that needs no decision, at its cause. Give a behavior defect a test at the cheapest layer that observes it: an `internal/claude` test through the agent's public methods, with a stand-in `claude` script for process I/O; an `internal/server` test with the fake harness; a `make e2e` spec; or a `TestLive` case with Haiku. The test fails before the fix. If a fix turns out to need a decision, revert it.
12. Ask the user to decide each finding that needs a decision, one focused question at a time, and wait for the answer. Ask as the user experiences Ox: what they would see happen, the options as they would experience them, and a recommendation, without code names. Fix it as the user decided.
13. Write each Recorded finding into `docs/known-issues.md`: its worst cost, how it was confirmed, and what it waits on. Remove each entry the review fixed or showed not to exist.
14. Run `make test` and `make e2e`. When the change or a fix alters how Ox drives Claude Code, also run `make live`. Fix failures before continuing, and report each check you could not run.

## Write the review

15. Read this skill's `assets/_template.md` and write the review in `.agents/reviews/`, named `YYYY-MM-DD-NNN-slug.md` with the next sequence for the day. Do not stage or commit it.
16. Write the commit messages the review's changes land with. For uncommitted changes, one message covers the change and its fixes. For committed code, each fix is its own change with its own message. Each message follows `AGENTS.md` and the style of `git log`: what was wrong and when it showed, what the code does now, the test that failed before, and for new state, the behavior that needs it, its races, and its cleanup. A message does not refer to the review or its finding IDs.

## Stage

17. Stage the fixes and the `docs/known-issues.md` changes, and for uncommitted changes, the change under review. Stage nothing else, and do not commit.
18. Give the final response and stop.

## Final response

Reply in the form below and nothing else. Lead with the main point, write plainly, and leave out the checks that passed, the suspected findings you ruled out, and the steps you took.

```markdown
One or two sentences giving the verdict and the number of findings by outcome.

| ID | Severity | Outcome  | Title                                 | Location                    |
| -- | -------- | -------- | ------------------------------------- | --------------------------- |
| F1 | High     | Fixed    | Relay accepts requests from any host  | `cmd/ox/main.go:41`         |
| F2 | Low      | Rejected | Reconnect reloads every history       | `cmd/ox/web/src/ox.tsx:285` |

**Review:** `.agents/reviews/YYYY-MM-DD-NNN-slug.md`

**Views to check:** the session view, the new-session view.

**Not run:** `make live`.

**Files:**

- `cmd/ox/main.go` (modified)
- `docs/known-issues.md` (modified)

**Next:** commit with the messages in the review.
```

The table lists every finding, most severe first; if there are none, replace it with `No findings.` List the web client views the change and its fixes touch under Views to check, for the user to check by eye, and omit the line when there are none. Omit Not run when every check ran. List every file the review created or modified, other than the review itself, marked `(created)` or `(modified)`.
