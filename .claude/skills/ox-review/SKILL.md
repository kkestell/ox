---
name: ox-review
description: Review Ox code in uncommitted changes, a commit, a branch, named files, or the whole repository, against its plan, Ox's functional requirements, and `AGENTS.md`, and write the review in `.agents/reviews/`, marking each confirmed finding to fix or to record, and each rejected one with its reason. Fixes nothing. Run again with an existing review to verify the fixes made for it. Do not use for plan critiques or standalone documentation reviews. Never use it unless the user, or an agent acting on the user's request, invokes it.
argument-hint: "[review scope and the plan it implements, or verify <review path>]"
---

Use this skill only when the user, or an agent acting on the user's request, invokes it. Never use it on your own initiative.

## Objective

Review the code and write a review that tells whoever implemented it exactly what to change. Give every finding one action, as `AGENTS.md` ("Defects") defines the outcomes:

- `Fix`: a confirmed defect, or a design, test, or documentation problem whose fix costs less than keeping it.
- `Record`: a confirmed defect that cannot be fixed yet, because it needs reproducing or depends on Claude Code behavior Ox cannot change. The implementer writes it into `docs/known-issues.md`.
- `Rejected`: speculative, a misreading of the intended behavior, or cheaper to keep than to fix, such as an unmeasured scaling cost. The reason is recorded so the finding is not raised again.

Change no code, tests, or documentation, and stage or commit nothing. Edit only the review file. Do not start other agents.

Correctness matters for what happens in normal use and for the requirements in `docs/functional-requirements.md`. Handling every case that could occur does not.

## Choose what to review

1. Read `<review_scope> $ARGUMENTS </review_scope>`. If it asks to verify an existing review, go to [Verify fixes](#verify-fixes). Ask the user only when the answer would change what you review.
2. For uncommitted changes, a commit, or a branch, review the changes and the code that calls or depends on them. For named files or the whole repository, review that full scope; for the whole repository, check every requirement in `docs/functional-requirements.md`. Record the base revision and the state of the working tree you reviewed.
3. Read `AGENTS.md`, `docs/functional-requirements.md`, and `docs/known-issues.md`; for web client code, also `cmd/ox/web/AGENTS.md`. Read the sections of `docs/architecture.md`, `docs/protocol.md`, and `docs/claude-code.md` that cover the code under review. If the scope names a plan in `.agents/plans/`, read it: its Goal, its Decisions, its Requirement changes, and its settled Check your assumptions entries are the requirements.

## Review

4. Trace behavior through callers, state owners, resource lifetimes, and tests. Judge implementation choices by their purpose and consequence. Do not treat a language feature or coding pattern as a defect on its own.
5. Check the code for each of these categories the scope reaches:
   - **Intent.** The change delivers the plan's Goal, keeps its Decisions and the user's answers, and stays within it. Behavior the user can observe changes only as the plan's Requirement changes say, and `docs/functional-requirements.md` carries exactly those changes, word for word. Observable behavior that changes without a requirement change, and a requirement edit the plan does not make, are findings.
   - **Requirements.** Each functional requirement the code touches, cited by title, holds through the whole lifecycle of a page's request: in flight, its reply lost, a reconnect, navigation away and back, a second page acting on the same session, the process exiting or parking, and `oxd` running a page's independent requests in any order. Text the user typed is never lost or sent twice. The page shows an action as possible, or a value as applied, only once `oxd` has confirmed it.
   - **Settings.** The Claude process runs with the model, effort, permission mode, and account the page shows, including after a partial failure, a restart, or with an inherited environment. A setting Claude would silently ignore or default is refused.
   - **Settling.** Each wait on a reply, a pipe, a process, or a child of Claude ends, by what it waits on or by a bound. No path leaves a request, a stop, or a shutdown without a result, and each error is shown where its request was made.
   - **Process I/O.** Claude's output is read to its last line, including one that ends at EOF; Claude's exit is seen even while a child holds its pipes; a failure reason on stdout or stderr reaches the request that failed.
   - **Ownership.** State is changed only by its owner: the session goroutine in `oxd`, the session's control on the page. Each new state, timer, retry, lock, or stored field is needed by a user-visible behavior, and its races and cleanup are accounted for. A race that can leave state the design says cannot exist is a defect of ownership, however unlikely.
   - **Construction.** The code the change adds cannot build a state the design forbids, as `AGENTS.md` ("Correct by construction") describes. Findings include fields valid only in one state held beside a flag or as zero values every reader checks, one fact stored in two places and kept in sync, a rule each caller decides for itself, input checked again past its boundary, and, in TypeScript, optional fields where a discriminated union fits.
   - **Claude Code.** Each assumption about Claude's output, ordering, timing, or flags matches `docs/claude-code.md` and handles every variant it records, or has a live check. A fact inferred from Claude's tool calls or hooks names the event that clears it. Updates are attributed to the turn that produced them.
   - **Security.** Listeners, the relay, file paths, process environments, and files outside Ox's own directories admit only the user and touch only what Ox owns.
   - **Correctness.** Other required behavior: results, state changes, and error reporting.
   - **Testing.** Each behavior the change adds or fixes has a kept test that fails without it, at the cheapest layer that observes it. Tests use the public interface of the code they test, and a live or e2e check waits for the event it claims to test, not an earlier one.
   - **Simplicity.** Code that adds no required behavior, including speculative fallbacks, retries, and recovery for failures that have not happened.
   - **Conventions.** The rules in `AGENTS.md` and `cmd/ox/web/AGENTS.md`, such as one name per field and log lines that name the session.
   - **Documentation.** Each changed fact is stated by the document `AGENTS.md` assigns it, and no document contradicts the code.
6. Confirm each suspected finding by tracing the code, reproducing it, or running a focused check: a stand-in `claude` script, an injected `/rpc` delay or failure in an e2e spec, or a probe against built binaries on a temporary socket, database, and port. Report missing handling only when a requirement or a reachable failure needs it. Do not ask for tests of hypothetical cases. Leave out what you cannot confirm. Revert every edit a check needs before you finish.
7. Skip a defect `docs/known-issues.md` records unless what its entry waits on has changed.
8. Run each test the change adds against the base revision, in a detached worktree that you remove afterwards; a new test that passes there checks nothing the change did. Then remove each condition the change adds, one at a time, and confirm a test fails. Each test that survives either check, and each condition no test protects, is a `Testing` finding.
9. Run the checks that cover the change: `make test` and `make e2e`; `make live` when the change alters how Ox drives Claude Code; and headless Chrome with Playwright through the `make dev` stack on spare ports when it changes how the page reaches `oxd`. Each failure is a finding. Report a check as blocked only when something you cannot change prevents it, and name the blocker.

## Write the review

10. Give each finding its action, as the Objective defines them. When the right fix raises a product question, a choice that changes what the user sees or does in everyday use that the plan and the user's answers do not settle, ask the user, one focused question at a time: what they would see happen, the options as they would experience them, and your recommendation, without code names. Wait for the answer, and make it the finding's `Fix`. Design, interface, and internal behavior choices, and how a rare case behaves, are yours; severity, size, and file count alone do not make a product question.
11. Read this skill's `assets/_template.md` and write the review in `.agents/reviews/`, named `YYYY-MM-DD-NNN-slug.md` with the next sequence for the day.
12. For each `Fix`, state the change to make at the cause, and the test to keep: its layer, the behavior it guards, and the existing test that should carry it when one already reaches the case. For each `Record`, state the entry: its worst cost, how it was confirmed, and what it waits on. For a fix the user decided, state their decision.
13. Give the final response and stop.

## Verify fixes

Run this when asked to verify the fixes for an existing review.

14. Read the review. Inspect the changes made since it was written, and the code each `Fix` and `Record` finding names.
15. For each `Fix` and `Record` finding, confirm the change addresses its cause, and that its test exists, fails with the fix removed, and passes with it. A `Record` finding is resolved by its `docs/known-issues.md` entry. Mark each finding `Resolved` or `Open`, with the evidence. Do not raise a `Rejected` finding again.
16. Review the changes made since the review as in [Review](#review), for regressions and new defects, and run the checks in step 9 again. Add each new finding with the next ID and its action.
17. Add a verification round to the review: the revision you checked, each finding's status, the new findings, and the checks run. Then give the final response and stop.

## Final response

Reply in the form below and nothing else, for a review and for a verification. Lead with the main point, write plainly, and leave out the checks that passed, the suspected findings you ruled out, and the steps you took.

```markdown
One or two sentences: the verdict, and the number of findings by action and, after verification, by status.

| ID | Severity | Action   | Status | Title                                | Location                    |
| -- | -------- | -------- | ------ | ------------------------------------ | --------------------------- |
| F1 | High     | Fix      | Open   | Relay accepts requests from any host | `cmd/ox/main.go:41`         |
| F2 | Low      | Rejected | —      | Reconnect reloads every history      | `cmd/ox/web/src/ox.tsx:285` |

**Review:** `.agents/reviews/YYYY-MM-DD-NNN-slug.md`

**Views changed:** the session view, the new-session view.

**Blocked:** `make live`: what blocks it.

**Verdict:** Fixes needed. (or: Ready to commit.)
```

List every finding, most severe first; if there are none, replace the table with `No findings.` List under Views changed the web client views the change touches, for the user to judge how they look, and omit the line when there are none. Omit Blocked when every check ran. The verdict is `Ready to commit` only when every `Fix` and `Record` finding is `Resolved` and every check passed.
