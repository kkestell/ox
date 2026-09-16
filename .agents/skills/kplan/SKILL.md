---
name: kplan
description: Explore an Ox code change, resolve material implementation decisions, and write a concise plan in `eng/plans/`. Use when the user wants to plan code work or a significant behavior change; do not use for standalone documentation or roadmap edits.
argument-hint: "[feature idea, bug report, or improvement to explore]"
---

## Workflow

This skill produces an implementation plan. It never implements the plan.

### Establish the work

1. Identify the contract that owns the work. Read `docs/spec.md` for
   user-visible behavior or a reported violation of product behavior,
   `eng/todo.md` for feature scope and ordering, and `eng/architecture.md` for a
   structural change. If a required owner document is missing, name the missing
   path and stop; do not create a placeholder or infer its contents from the
   request.
2. Resolve `<feature_description> $ARGUMENTS </feature_description>` and
   identify the work.
   - Plan a defect repair against its owning contract and the code. A bug
     report, review finding, regression, or contradiction of the specification
     needs no TODO entry, including inside a completed task.
   - Plan feature work from `eng/todo.md`. If the description names an unchecked
     task, use it. Without a description, select the first unchecked task in
     file order, descending to its first unchecked subtask when present.
   - If feature work adds scope absent from the TODO, conflicts with its order,
     or no unchecked task exists, stop and identify the decision the user must
     make in `eng/todo.md`. Do not edit the TODO in this skill.
3. Read the owner documents identified above. Consult
   `~/src/references/index.md` before planning a substantial capability, as
   required by the repository instructions.
4. Confirm that the source documents settle the work. If observable behavior is
   missing or ambiguous, stop and ask the user to settle it in `docs/spec.md`.
   Confirm reported defects against their owning contract and code. Resolve only
   decisions those sources and established repository patterns leave open.

### Explore and size

5. Inspect the relevant attachment points, adjacent patterns, public boundaries,
   and tests with targeted searches and file ranges. Follow an established local
   pattern when it settles the design; compare alternatives only when there is a
   real choice.
6. Write one plan for the selected work. Scope a feature plan from its TODO task
   and a defect plan from the confirmed violation. State the starting state,
   dependencies, and unfinished integration only when they affect
   implementation. Identify when the plan would finish a top-level TODO item,
   because that boundary requires the repository's completion review.

### Write

7. Name the plan `eng/plans/YYYY-MM-DD-slug.md`; add a sequence only when it is
   needed to disambiguate plans from the same day.
8. Use `eng/plans/TEMPLATE.md` as a scaffold. Keep only source references,
   concrete file-oriented tasks, non-obvious decisions, and change-specific
   tests. Do not restate the TODO, architecture, repository rules, standard
   validation commands, conversation history, rejected alternatives, or work
   owned by a later TODO task.
9. Print the final plan path and stop. Do not review the plan locally or with a
   subagent.
