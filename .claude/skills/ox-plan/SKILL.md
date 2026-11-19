---
name: ox-plan
description: Plan a change to Ox, given a request, bug report, todo item, or known issue, and write the plan in `.agents/plans/`. The plan settles every decision except product questions, which the user answers, and accounts for the functional requirements, state owners, Claude Code behavior, and tests the change depends on. Use before implementing a code or behavior change to Ox, not for standalone documentation edits. Never use it unless the user, or an agent acting on the user's request, invokes it.
argument-hint: "[request, bug report, todo item, or known issue]"
---

Use this skill only when the user, or an agent acting on the user's request, invokes it. Never use it on your own initiative.

## Objective

Write a plan for one change to Ox without changing the code. Whoever implements the plan follows it directly, and `/ox-review` checks the result against it.

## Establish the work

1. Read `<request> $ARGUMENTS </request>`. If it is empty, ask the user what to plan and stop. If it names an item in `docs/todo.md` or `docs/known-issues.md`, read that item.
2. Read `AGENTS.md`, `docs/functional-requirements.md`, and `docs/known-issues.md`. For web client work, also read `cmd/ox/web/AGENTS.md`. Read the sections of `docs/architecture.md`, `docs/protocol.md`, and `docs/claude-code.md` that cover the code the change touches.

## Explore the code

3. Read the code to change, its callers, nearby examples, and its tests. Look beyond the files to change: whether Ox already does this, whether an existing owner or abstraction has the wrong shape for the change, and whether the change extends the architecture or sits bolted on. Follow an existing pattern when it is sound. For a defect, find its structural cause: the representation, duplicated fact, scattered rule, or owner that let the wrong state exist. Search the code for every other place with the same structure; those places belong to this change.
4. Trace the change across every boundary it crosses: page, relay, `oxd` connection, session goroutine, harness agent, and Claude process. Name the owner of each piece of state it reads or writes.

## Settle the request

5. A plan covers one change, which lands as one commit. If the request holds independent changes, or leaves open what the change should do, its scope, or its priorities, ask the user in one message, in terms of what they would see in Ox, with your recommendation, and wait. Plan once they answer.

## Decide

6. Settle every other decision yourself except product questions. A product question is a choice that changes what the user sees or does in everyday use of Ox. Internal design, which component owns a rule, protocol and data shapes, removing code that cannot run, and how rare cases behave are yours. Handle rare cases the simplest reasonable way; one becomes a product question only when it changes what the user sees in everyday use.
7. Choose the option that leaves Ox simplest overall, which may move ownership, rewrite an abstraction, or delete code rather than make the smallest change. Prefer a design in which wrong states cannot be represented over one that checks for them, as `AGENTS.md` ("Correct by construction") describes. When the change repairs a defect, plan the fix at its structural cause so its whole class cannot recur, at every place the cause appears. Plan a guard, special case, or retry at the symptom only when the cause lies outside Ox, and name that cause. A race that can leave state the design says cannot exist is fixed by changing who owns the state, not by handling the interleaving.
8. Account for each of these concerns the change touches, and record what it requires under Decisions:
   - **Requirements.** Each requirement in `docs/functional-requirements.md` the change touches, by its title, and how it holds through the whole lifecycle of a page's request: in flight, its reply lost, a reconnect, navigation away and back, a second page acting on the same session, the process exiting or parking, and `oxd` running a page's independent requests in any order. The page shows an action as possible, or a value as applied, only once `oxd` has confirmed it.
   - **Settings.** Whatever reaches Claude's flags, environment, or control requests leaves the process running with exactly the model, effort, permission mode, and account the page shows, including after a partial failure or a restart. A setting Claude would silently ignore or default is refused.
   - **Settling.** Each wait the change adds, on a reply, a pipe, a process, or a child of Claude, ends: by what it waits on or by a bound. Every path gives the request a result or an error, shown where the request was made.
   - **Process I/O.** Claude's output is read to its last line, including one that ends at EOF; Claude's exit is seen even while a child holds its pipes; a failure reason on stdout or stderr reaches the request that failed.
   - **Construction.** Each invariant the change adds or relies on, and the type, owner, or single source that enforces it, so that no reader has to check it. For a defect, the structure that let the wrong state exist, every place that structure appears, and how the change makes the state impossible to build.
   - **Ownership.** For each state, timer, retry, lock, or stored field the change adds: its owner, the user-visible behavior that needs it, the races it brings, and its cleanup. The commit body repeats this.
   - **Claude Code.** Each assumption about Claude's output, ordering, timing, or flags, with the section of `docs/claude-code.md` that records it. The plan handles every variant that section records. An assumption it does not record becomes a live check task whose result goes into `docs/claude-code.md`. A fact inferred from Claude's tool calls or hooks names the event that clears it.
   - **Trust boundaries.** For a change to a listener, the relay, a file path, a process environment, or a file outside Ox's own directories: who can reach it and what it can touch.
9. Choose the tests at the cheapest layer that observes each behavior:
    - Claude harness logic: `internal/claude` tests that build state through `Send` and `Handle` with real Claude messages and assert through the agent's public methods, recorded updates, and bytes written. Process I/O uses a stand-in `claude` script.
    - Daemon behavior: `internal/server` tests that speak the protocol with the fake harness. A server path the fake harness does not reach needs the fake to reach it, or a live case.
    - Page behavior: `make e2e` specs, with delays and failures injected by intercepting the page's `/rpc` posts.
    - The path from the page to `oxd` through the relay and Vite's proxy: a `cmd/ox` test of the headers the relay receives, and a check in headless Chrome through the `make dev` stack on spare ports.
    - Claude Code behavior: a small `TestLive` case or `approval.spec.ts` check with Haiku.

    Put each case in an existing test whose setup already reaches it, and add a new test only when none does. Each new test fails on the code before the change, and each condition the change adds has a test that fails when the condition is removed. A change that replaces behavior replaces the tests of the old behavior.

## Write

10. Read this skill's `assets/_template.md` and write the plan in `.agents/plans/`, named `YYYY-MM-DD-NNN-slug.md` with the next sequence for the day. Do not stage or commit it.
11. Keep the plan to relevant code references, decisions that need explaining, tasks tied to files, names, and tests. Leave out repository rules, standard validation commands, conversation history, rejected options, and work for a later change.
12. Use the protocol's names in tasks, code, comments, and documentation. Do not give one concept several names or give an existing term a new meaning.
13. Under Requirement changes, give the exact text of each requirement the change adds, changes, or removes in `docs/functional-requirements.md`, in that file's form: a short bold title, then one or two plain sentences about what the user sees or relies on. A change alters the requirements only when it adds, alters, or removes behavior the user can observe; a bug fix changes none unless it shows a requirement was wrong. Write `None.` when nothing changes.
14. Under Documentation updates, name the document `AGENTS.md` assigns each other fact the change alters. Leave out documents whose facts do not change.
15. Under Check your assumptions, list the product questions the request leaves open. Each requirement the plan changes or removes is one, quoting the old and new text. Most plans have none, one, or two; write `None.` when nothing qualifies. Write each as the user experiences Ox: what the plan does that they will notice, and the alternative with what it would cost them, without code names.
16. Do not review the plan yourself or ask another agent to review it.

## Check your assumptions

17. If the list is `None.`, give the final response. Otherwise, present the entries to the user in one message, numbered, most consequential first, in the same form as in the plan. The user answers only the entries they want changed; the rest stand. Wait for the answer.
18. Edit the affected lines of the plan for each changed entry; do not rewrite the plan. Mark each entry kept or changed, and to what. If a change reopens a product question, present it the same way. Then give the final response.

## Final response

Reply in the form below and nothing else. Lead with the main point, write plainly, and leave out the options you rejected, the code you explored, and the steps you took.

```markdown
Two to four sentences explaining, at a high level, what the change does and how.

**Plan:** `.agents/plans/YYYY-MM-DD-NNN-slug.md`

**Requirements touched:** Stop, Lost connection

**Requirement changes:** added Retry a failed send; changed Stop.

**Files:**

- `internal/claude/claude.go` (modified)
- `cmd/ox/web/src/ox.tsx` (modified)
```

Name requirements by title. Write `None.` for Requirements touched or Requirement changes when there are none. List every file the plan will create or modify, marked `(created)` or `(modified)`.
