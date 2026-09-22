# Readability

- Follow the operation as a reader: state changes, ownership, and authority
  decisions should be visible near their use. Multiple representations or
  scattered flags may make reasoning harder than a longer coherent function.
- Helpers and other indirection should name concepts or remove complexity.
  Prefer direct control flow when indirection hides a simple decision.
- Comments should explain a non-obvious reason or contract. Avoid narration
  and blanket documentation demands for self-explanatory code.
