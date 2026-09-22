# Plan title

A plan is a settled work order, not a record of exploration. Keep only the
sections that communicate an implementation decision, source reference, task, or
change-specific test. Do not retain empty headings or template prompts.

## Goal

State the problem and observable outcome.

## Related code

- `path/to/file` — Why this file, contract, or established pattern matters.

## Decisions

Record only non-obvious choices that implementation must preserve. Include the
current constraint or architectural fit when it makes a task intelligible.

## Naming

List every domain term the plan uses, with its `eng/glossary.md` meaning or
its new definition. Use glossary terms verbatim in tasks, code names,
comments, and documentation. Do not introduce synonyms or overload an existing
term.

- `term` — Glossary meaning or new definition, and where the name appears.

## Test plan

- Stable boundary and behavior to prove, including material error paths.

## Implementation plan

- Concrete task in execution order.

## Documentation updates

- Owned current-state documentation to update when the work lands. Omit this
  section when none applies.
