---
name: kplan
description: Explore an Ox code change, resolve material implementation decisions, and write a concise plan in `eng/plans/`. Use when the user wants to plan code work or a significant behavior change; do not use for standalone documentation edits.
argument-hint: "[feature idea, bug report, or improvement to explore]"
---

## Workflow

This skill produces an implementation plan. It never implements the plan.

### Establish the work

1. Resolve `<feature_description> $ARGUMENTS </feature_description>` and
   identify the work. If the description is empty, ask what the user wants
   to plan and stop.
2. Read `AGENTS.md`, `eng/architecture.md`, `eng/code-style.md`,
   `eng/glossary.md`, and `eng/testing.md` before exploring
   implementation. Reference those sources from the plan. Do not restate
   their contents.
3. Confirm that the request settles the work. Ask one focused question
   only when the next work remains ambiguous. Resolve only decisions the
   owning documents and established repository patterns leave open.

### Explore and size

4. Inspect the relevant attachment points, adjacent patterns, public
   boundaries, and tests with targeted searches and file ranges. Follow an
   established local pattern when it settles the design; compare
   alternatives only when there is a real choice.
5. Write one plan for the selected work. State the starting state,
   dependencies, and unfinished integration only when they affect
   implementation.

### Naming

6. Consult `eng/glossary.md` before writing. Use its terms verbatim in the
   plan, and shape task names, code names, comments, and documentation
   around the same vocabulary. Do not introduce synonyms or overload an
   existing term. When the work needs a new term, define it once in the
   plan Naming section and use it consistently everywhere.

### Write

7. Name the plan `eng/plans/YYYY-MM-DD-NNN-slug.md`, using the next
   sequence for the day.
8. Use `eng/plans/TEMPLATE.md` as a scaffold. Keep only source references,
   concrete file-oriented tasks, non-obvious decisions, naming decisions,
   and change-specific tests. Do not restate the architecture, repository
   rules, standard validation commands, conversation history, rejected
   alternatives, or work owned by a later change.
9. Print the final plan path and stop. Do not review the plan locally or
   with a subagent.
