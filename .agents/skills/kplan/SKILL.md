---
name: kplan
description: Explore an Ox code or behavior change and write a plan in `eng/plans/`. Use for implementation planning, not standalone documentation edits.
argument-hint: "[feature idea, bug report, or improvement to explore]"
---

## Workflow

Write a plan without changing the code.

### Establish the work

1. Read `<feature_description> $ARGUMENTS </feature_description>`. If it is
   empty, ask what the user wants to plan and stop.
2. Read `AGENTS.md`, `eng/architecture.md`, `eng/code-style.md`,
   `eng/glossary.md`, and `eng/testing.md` before exploring the code.
   Refer to these documents in the plan without repeating their contents.
3. Check that the request makes the scope clear. Ask one focused question only
   if the scope remains unclear. Use the answer in the repository documents or
   existing code when one exists.

### Explore the code

4. Search the relevant files and read the code to change, nearby examples,
   public APIs, and tests. Follow an existing pattern when it fits. Compare
   options only when a choice remains.
5. Write one plan. Describe current behavior, dependencies, or unfinished work
   only when they affect the implementation.

### Naming

6. Use terms from `eng/glossary.md` exactly. Use the same names in plan tasks,
   proposed code, comments, and documentation. Do not give one concept several
   names or give an existing term a new meaning. Define any new term in the
   plan's Naming section and use it consistently.

### Write

7. Read `eng/plans/TEMPLATE.md` and use it to write the plan in `eng/plans/`.
   Name the file `YYYY-MM-DD-NNN-slug.md`, using the next sequence for the day.
8. Keep the plan to relevant code references, tasks tied to files, decisions
   that need explanation, names, and tests for this change. Do not repeat the
   architecture documents, repository rules, standard validation commands,
   conversation history, rejected options, or work for a later change.
9. Give the final plan path and stop. Do not review the plan yourself or ask
   another agent to review it.
