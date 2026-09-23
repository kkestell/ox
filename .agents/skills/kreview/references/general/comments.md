# Comments

Apply this lens to comments in the code. Use the documentation lens for caller
guidance and associated design documents.

A comment should tell the reader something important that the code cannot tell
them clearly. Ask whether deleting it would make a future reader more likely
to misunderstand or break the code.

- Look for explanations of intent, constraints that must remain true, surprising
  behavior, and why an obvious alternative fails. Flag missing explanations
  when the code leaves a concrete maintenance risk unclear; do not require a
  comment on every item.
- Prefer a clearer name or simpler structure when it would make an explanation
  unnecessary. Do not contort readable code to avoid explaining a subtle idea.
- Identify narration of obvious code, vague claims, and history that belongs in
  version control when they obscure useful information or invite confusion.
  Preserve brief summaries that help readers understand a complex operation.
- Check claims against the implementation and its callers. Stale or misleading
  comments can be worse than missing ones. Keep explanations close to the code
  they govern, and update them when that code changes.
- Workaround comments should explain the external constraint, link the relevant
  issue when available, and say when the workaround can be removed.
- Support findings with the misleading claim, missing reason, or distracting
  text and its consequence for the reader. Recommend the smallest useful fix:
  clarify the code, add or correct an explanation, or delete an unhelpful
  comment. Do not manufacture findings from wording preferences alone.
