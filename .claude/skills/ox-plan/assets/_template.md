# Plan title

Write the plan after making the main decisions. Keep the sections needed to explain the change, the relevant code, the tasks, and the tests. Delete unused headings and placeholder text, including this guidance.

## Goal

The problem, and what should happen when the change is done. Link the todo item, known issue, or audit finding it comes from, if any.

## Related code

- `path/to/file` — What this code does and why it matters to the change.

## Decisions

Choices that may not be clear from the tasks, and what each concern the change touches requires. Omit the concerns it does not touch.

- **Requirements:** each functional requirement by title, and how it holds through the request lifecycle.
- **Settings:** how the process keeps running with what the page shows.
- **Settling:** each new wait, and what ends it.
- **Process I/O:** how output, exit, and failure reasons reach their reader.
- **Construction:** each invariant and the type, owner, or single source that enforces it.
- **Ownership:** for each new state, timer, retry, lock, or stored field, its owner, the user-visible behavior that needs it, its races, and its cleanup.
- **Claude Code:** each assumption, with its `docs/harnesses/claude-code.md` section and the variants it records, or the live check that settles it.
- **Trust boundaries:** who can reach a changed listener, path, or environment, and what it can touch.

## Check your assumptions

Product questions only, most consequential first, or `None.`. Write them in the user's terms, without code names. Once the user has settled the list, mark each entry kept or changed, and to what.

1. **The choice, in a few plain words.**
   - **Plan:** What the user will see or do with this choice, and why it was chosen, in a sentence or two.
   - **Alternative:** What the user would see or do instead, and what it would cost them.

## Test plan

- Behavior or failure case, its layer (Claude harness, server, e2e, or live), and the existing test that gets the assertion. For a new test, the behavior it guards and why no existing test can carry it.

## Implementation plan

- Task to complete, tied to its files, in the order it should be done.

## Requirement changes

- **Added:** the requirement's full text, title first, as it will appear in `docs/functional-requirements.md`.
- **Changed:** the title, the old text, and the new text.
- **Removed:** the title, and why the behavior goes.

Write `None.` when the change adds, alters, and removes no behavior the user can observe.

## Documentation updates

- The owning document for each other fact the change alters. Omit this section if there is none.
