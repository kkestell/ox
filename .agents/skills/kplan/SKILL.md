---
name: kplan
description: Explore an Ox code or behavior change and write a plan in `eng/plans/`. Use for implementation planning, not standalone documentation edits.
argument-hint: "[jira ticket, feature idea, bug report, or improvement to explore]"
---

## Objective

Write a plan without changing the code.

## Establish the work

1. Read `<feature_description> $ARGUMENTS </feature_description>`. If it is
   empty, ask what the user wants to plan and stop.
2. Read `AGENTS.md`, `eng/architecture.md`, `eng/code-style.md`,
   `eng/glossary.md`, and `eng/testing.md` before exploring the code.
   Refer to these documents in the plan without repeating their contents.

## Explore the code

3. Search the relevant files and read the code to change, nearby examples,
   public APIs, and tests. Follow an existing pattern when it fits. If the
   request is too unclear to know where to look, brainstorm first.

## Brainstorm

4. Brainstorm with the user before writing when the user asks to brainstorm,
   the request is unclear, or exploring leaves a real engineering or design
   decision. Otherwise, go to Write.
5. Ask one focused question at a time and wait for the answer. For each
   decision, give the options, their trade-offs, and a recommendation. Do not
   ask what the repository documents or existing code already answer.
6. Speak clearly and simply. Use terms from `eng/glossary.md` exactly, and do
   not use jargon, invented terms, or shorthand.
7. Say so directly when a request adds a lot of complexity, contradicts the
   architecture or an earlier decision, or fits poorly with existing code.
   Name the cost and recommend a simpler option. Do not let the design grow
   complex to fit every request.
8. Stop brainstorming when the scope and every decision are settled.

## Write

9. Read `eng/plans/TEMPLATE.md` and use it to write the plan in `eng/plans/`.
   Name the file `YYYY-MM-DD-NNN-slug.md`, using the next sequence for the day.
10. Keep the plan to relevant code references, tasks tied to files, decisions
    that need explanation, names, and tests for this change. Do not repeat the
    architecture documents, repository rules, standard validation commands,
    conversation history, rejected options, or work for a later change.
11. Use terms from `eng/glossary.md` exactly. Use the same names in plan tasks,
    proposed code, comments, and documentation. Do not give one concept several
    names or give an existing term a new meaning. Define any new term in the
    plan's Naming section and use it consistently.
12. Give the final plan path and stop. Do not review the plan yourself or ask
    another agent to review it.
