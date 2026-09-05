# Root Workspace Instructions

## Sources

- `docs/spec.md#workspace-instructions-and-skills` — root-only discovery,
  validation limits, activation lifetime, and parent/child behavior
- `eng/roadmap.md#root-instructions` — slice scope and completion gates
- `eng/architecture.md#configuration-and-credentials` and
  `eng/architecture.md#context-and-durable-projections` — activation inputs,
  immutable turn configuration, and durable recovery ownership
- `~/src/references/repos/personal/eta/internal/agent/{prompt,prompt_test}.go` —
  workspace-instruction prompt block and focused composition tests to adapt
- `internal/agent/{prompt,agent,state}.go` — prompt composition, activation,
  persisted request configuration, checkpoint, and recovered-turn seams
- `internal/workspace/{workspace,platform}.go` — confined file-opening and
  regular-file validation patterns

## Goal

Load the session root's `AGENTS.md` as a bounded, activation-frozen instruction
source for both the parent and its children. Invalid instruction files fail
activation, while durable turn configuration preserves the exact instructions
used by work that later recovers.

## Implementation

- `internal/agent/prompt.go` — add a focused root-instruction loader that checks
  only `<canonical-cwd>/AGENTS.md`. Treat absence as no instructions; reject an
  unreadable, symlinked, non-regular, larger-than-64-KiB, or invalid-UTF-8 file
  with an error naming that path. Use a bounded, nonblocking, symlink-refusing
  open so validation cannot hang on a special file or read outside the root.
- `internal/agent/prompt.go` — extend parent and subagent composition to append
  the same validated content in a distinct `<workspace-instructions>` system
  block. Do not search parent directories or nested workspace paths, and do not
  turn instructions into conversation or ACP replay content.
- `internal/agent/agent.go` — resolve instructions once per new/load/resume
  activation before constructing both prompts. Let the existing persisted
  `requestConfiguration` and activation configuration-change record retain the
  resulting parent and child prompts. A later activation therefore captures a
  changed file, while an open turn and its recovered permission/tool work keep
  the prompts frozen in `openTurnConfiguration`.
- `internal/workspace` — expose only the minimal root-confined read primitive
  needed if the agent loader cannot enforce no-symlink, regular-file, bounded
  reads with the existing workspace APIs; keep instruction policy in the agent.

## Tests

- Prompt and loader unit tests cover missing and empty files, the 64-KiB
  boundary, oversize input, invalid UTF-8, unreadable files, directories and
  other non-regular files, symlinks, exact path-bearing errors, and identical
  parent/child instruction blocks.
- Unit or integration tests prove that an `AGENTS.md` above the canonical root
  and one in a nested directory are ignored, while the root file is included
  exactly once without entering model-visible conversation history.
- Fake-model integration tests capture parent and delegated-child requests,
  prove edits to `AGENTS.md` do not affect an active session, and prove
  close/load or resume captures changed instructions for subsequent turns.
- Recovery tests change `AGENTS.md` after a turn reaches a durable permission
  wait, restart the runtime, and prove resumed parent and child requests use the
  original instructions. The next newly admitted turn uses the reactivated
  content, including after checkpoint recovery and compaction.
- Real-binary activation tests exercise the invalid-file cases and show a
  rejected session does not prevent a valid session from being created.

## Decisions

- Persist no second instruction value or identity. The already-durable parent
  and subagent system prompts are the immutable turn input, so request
  admission, compaction, checkpoints, and interrupted-turn recovery all consume
  one authoritative representation.
- Preserve the validated file text when placing it in the prompt block rather
  than normalizing its line endings or trailing newline.
