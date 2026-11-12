# Ox: work intake, task queues, and user-defined workflows

Discussion notes, November 11, 2026.

## The problem

A workspace represents a project, usually software but sometimes documents.
Creating sessions and talking directly to agents is straightforward, but the
user has to coordinate all those sessions by hand, which becomes burdensome
when there is a backlog of upcoming work.

Parallel implementation has practical limits because changes can conflict.
Research, brainstorming, and planning for upcoming work can often proceed
while an agent implements the current task. The app should help organize that
concurrency.

## Requirements

- An intake agent the user can talk to informally, including rattling off
  incomplete ideas, which helps turn them into organized work.
- A task queue managed by that agent. Task types correspond to workflows, and
  tasks can run concurrently.
- Deterministic workflow execution containing probabilistic agent-session
  steps. The runner enforces required handoffs; they do not depend on an agent
  remembering a natural-language instruction.
- Workflows that users define themselves. The application has no fixed
  catalog of workflows and no single lifecycle that all work follows.
- A small set of carefully chosen building blocks that compose into different
  workflows. The right building blocks are not yet established.

## Interactive work

Tasks are not necessarily hands-off. A vague request can lead to a
brainstorming task with substantial back-and-forth between the user and an
agent. That conversation has its own context, so its details stay out of the
intake agent's context.

The user participates in these tasks without juggling raw agent sessions. The
interaction design should do more than drop the user into a chat; the
specific UX is undecided.

## Outcomes and subsequent work

Brainstorming can end with abandoning an idea or with a brief that supports
further planning. Workflows feed their results into other workflows or tasks.

A required handoff, such as moving a finished brief into planning, is a rule
the runner applies, not a decision left to the intake agent.

One possible direction puts explicit outcomes and transitions in a
user-authored workflow definition. For example, one workflow could route a
`proceed` outcome into planning and an `abandon` outcome to an end. These are
example branches, not application-wide task states or built-in workflow types.
No concrete workflow representation or execution model is settled.

## Open questions

- The composable building blocks, and how users define them.
- How an interactive step declares its outcome and completes.
- How outputs become inputs to subsequent tasks or workflows.
- The boundary between intake's judgment and the runner's explicit rules.
- How concurrency rules express which work can proceed together.
- How the UI presents participation requests and focused task interactions.
