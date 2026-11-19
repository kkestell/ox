---
name: ox-plan
description: Plan a change to Ox, given a request, bug report, todo item, or known issue, and write the plan in `.agents/plans/`. The plan settles every decision except product questions, which the user answers, and accounts for the guarantees, state owners, Claude Code behavior, and tests the change depends on. Use before implementing a code or behavior change to Ox, not for standalone documentation edits. Never use it unless the user invokes it or explicitly asks for it.
argument-hint: "[request, bug report, todo item, or known issue]"
---

Use this skill only when the user invokes it or explicitly asks for it. Never use it on your own initiative.

## Objective

Write a plan for one change to Ox without changing the code. Whoever implements the plan follows it directly, and `/ox-review` checks the result against it.

## Establish the work

1. Read `<request> $ARGUMENTS </request>`. If it is empty, ask what the user wants to plan and stop. If it names an item in `docs/todo.md` or `docs/known-issues.md`, read that item.
2. Read `AGENTS.md`, `docs/guarantees.md`, and `docs/known-issues.md`. For web client work, also read `cmd/ox/web/AGENTS.md`. Read the sections of `docs/architecture.md`, `docs/protocol.md`, and `docs/claude-code.md` that cover the code the change touches.

## Explore the code

3. Read the code to change, its callers, nearby examples, and its tests. Look beyond the files to change: whether Ox already does this, whether an existing owner or abstraction has the wrong shape for the change, and whether the change extends the architecture or sits bolted on. Follow an existing pattern when it is sound.
4. Trace the change across every boundary it crosses: page, relay, `oxd` connection, session goroutine, harness agent, and Claude process. Name the owner of each piece of state it reads or writes.

## Settle the request

5. A plan covers one change, which lands as one commit. If the request holds independent changes, tell the user which and plan the one they choose, or each in its own plan.
6. If the request leaves open what the change should do, its scope, or its priorities, ask the user those questions in one message, in terms of what they would see in Ox, and wait. Plan once they are answered.

## Decide

7. Settle every other decision yourself except product questions. A product question is a choice that changes what the user sees or does in everyday use of Ox. Internal design, which component owns a rule, protocol and data shapes, removing code that cannot run, and how rare cases behave are yours. Handle rare cases the simplest reasonable way; one becomes a product question only when it changes what the user sees in everyday use.
8. Choose the option that leaves Ox simplest overall, which may move ownership, rewrite an abstraction, or delete code rather than make the smallest change. When the change repairs a defect, plan the fix at its cause. A race that can leave state the design says cannot exist is fixed by changing who owns the state, not by handling the interleaving.
9. Account for each of these concerns the change touches, and record what it requires under Decisions:
   - **Guarantees.** Each guarantee in `docs/guarantees.md` the change touches, and how it holds through the whole lifecycle of a page's request: in flight, its reply lost, a reconnect, navigation away and back, a second page acting on the same session, the process exiting or parking, and `oxd` running a page's independent requests in any order. The page shows an action as possible, or a value as applied, only once `oxd` has confirmed it.
   - **Settings.** Whatever reaches Claude's flags, environment, or control requests leaves the process running with exactly the model, effort, permission mode, and account the page shows, including after a partial failure or a restart. A setting Claude would silently ignore or default is refused.
   - **Settling.** Each wait the change adds, on a reply, a pipe, a process, or a child of Claude, ends: by what it waits on or by a bound. Every path gives the request a result or an error, shown where the request was made.
   - **Process I/O.** Claude's output is read to its last line, including one that ends at EOF; Claude's exit is seen even while a child holds its pipes; a failure reason on stdout or stderr reaches the request that failed.
   - **Ownership.** For each state, timer, retry, lock, or stored field the change adds: its owner, the user-visible behavior that needs it, the races it brings, and its cleanup. The commit body repeats this.
   - **Claude Code.** Each assumption about Claude's output, ordering, timing, or flags, with the section of `docs/claude-code.md` that records it. The plan handles every variant that section records. An assumption it does not record becomes a live check task whose result goes into `docs/claude-code.md`. A fact inferred from Claude's tool calls or hooks names the event that clears it.
   - **Trust boundaries.** For a change to a listener, the relay, a file path, a process environment, or a file outside Ox's own directories: who can reach it and what it can touch.
10. Choose the tests at the cheapest layer that observes each behavior:
    - Claude harness logic: `internal/claude` tests that build state through `Send` and `Handle` with real Claude messages and assert through the agent's public methods, recorded updates, and bytes written. Process I/O uses a stand-in `claude` script.
    - Daemon behavior: `internal/server` tests that speak the protocol with the fake harness. A server path the fake harness does not reach needs the fake to reach it, or a live case.
    - Page behavior: `make e2e` specs, with delays and failures injected by intercepting the page's `/rpc` posts.
    - Claude Code behavior: a small `TestLive` case or `approval.spec.ts` check with Haiku.

    Put each case in an existing test whose setup already reaches it, and add a new test only when none does. Each new test fails on the code before the change, and each condition the change adds has a test that fails when the condition is removed. A change that replaces behavior replaces the tests of the old behavior.

## Write

11. Read this skill's `assets/_template.md` and write the plan in `.agents/plans/`, named `YYYY-MM-DD-NNN-slug.md` with the next sequence for the day. Do not stage or commit it.
12. Keep the plan to relevant code references, decisions that need explaining, tasks tied to files, names, and tests. Leave out repository rules, standard validation commands, conversation history, rejected options, and work for a later change.
13. Use the protocol's names in tasks, code, comments, and documentation. Do not give one concept several names or give an existing term a new meaning.
14. Under Documentation updates, name the document `AGENTS.md` assigns each fact the change alters. Leave out documents whose facts do not change.
15. Under Check your assumptions, list the product questions the request leaves open. Most plans have none, one, or two; write `None.` when nothing qualifies. Write each as the user experiences Ox: what the plan does that they will notice, and the alternative with what it would cost them, without code names.
16. Do not review the plan yourself or ask another agent to review it.

## Check your assumptions

17. If the list is `None.`, give the final response. Otherwise, present the entries to the user in one message, numbered, most consequential first, in the same form as in the plan. The user answers only the entries they want changed; the rest stand. Wait for the answer.
18. Edit the affected lines of the plan for each changed entry; do not rewrite the plan. Mark each entry kept or changed, and to what. If a change reopens a product question, present it the same way.

## Final response

Reply in the form below and nothing else. Lead with the main point, write plainly, and leave out the options you rejected, the code you explored, and the steps you took.

```markdown
Two to four sentences explaining, at a high level, what the change does and how.

**Plan:** `.agents/plans/YYYY-MM-DD-NNN-slug.md`

**Guarantees touched:** 1, 4

**Files:**

- `internal/claude/claude.go` (modified)
- `cmd/ox/web/src/ox.tsx` (modified)

**Next:** implement the plan, then `/ox-review uncommitted changes for .agents/plans/YYYY-MM-DD-NNN-slug.md`
```

Write `None.` for Guarantees touched when the change touches none. List every file the plan will create or modify, marked `(created)` or `(modified)`.
