# Conservative shell-rule suggestion

## Goal

`shellrules.Suggest` can hand the user an allow-always rule that authorizes far
more than the command they approved. `sh -c '...'` derives the bare rule `sh`,
which then matches any shell invocation, and the `strings.Fields` fallback can
derive `*`, which matches everything. Both grants last for the activation.

## Desired outcome

Ox offers a reusable shell grant only when it can derive a literal command
prefix that means what the user saw. Every other command falls back to
allow-once, which the permission path already does when the suggestion is empty.

## Summary of approach

Derive the rule only from the parsed first invocation, never from raw fields.
Refuse when the program word is not a literal, when it names a shell, language
runtime, or wrapper that runs another program, and when any later argument is
not a literal. Keep the existing prefix shape otherwise: program plus its first
non-flag literal argument.

## Related code

- `internal/shellrules/shellrules.go` - `Suggest`, `firstCall`, `literalValue`.
- `internal/tools/shell.go` - `shellSuggestion` passes the command through.
- `internal/agent/loop.go` and `internal/agent/approval.go` - an empty
  suggestion already hides the allow-always option and downgrades a stale
  allow-always decision to allow-once.

## Current state

- Relevant existing behavior: `Allowed` matches a rule as a literal word prefix
  with `*` as a per-word wildcard, so a short rule authorizes every longer
  command that starts with it.
- Existing patterns to follow: `literalValue` already refuses globs, expansions,
  and substitutions, so the parsed path fails closed.
- Constraints from the current implementation: no caller distinguishes "no
  suggestion" from "suggestion failed", so returning an empty string is the
  whole signal.

## Test plan

- **Key behaviors to verify:** interpreters and wrappers yield no rule, a
  wildcard or unparsable command yields no rule, a non-literal argument anywhere
  in the invocation yields no rule, and ordinary commands keep their current
  prefix.
- **Test levels:** unit, in `internal/shellrules`.
- **Edge cases and failure modes:** an interpreter reached by absolute path, a
  glob as the program word, a glob or expansion as a later argument, and a parse
  failure.
- **What not to test:** `Allowed`, which this change does not touch.

## Implementation plan

- Replace the `strings.Fields` fallback in `Suggest` with a closed failure.
- Refuse a program word whose base name names a shell, language runtime, or
  command wrapper.
- Refuse when any argument of the first invocation is not a literal.
- Extend the `Suggest` table with interpreter, wrapper, wildcard, non-literal,
  and parse-failure cases.

## Documentation updates

- `docs/spec.md` gains one sentence on when Ox offers a reusable shell grant.
- Todo list item "Harden reusable shell-rule derivation (F02)".

## Impact assessment

- Code paths affected: shell tool permission suggestion only.
- Data, protocol, or schema impact: none. Rules already stored stay valid.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the extended `TestSuggest`, then the package tests and
  the agent and integration suites.
- Static checks: `make check-go`.
