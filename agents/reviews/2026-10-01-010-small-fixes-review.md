# Small fixes review

## Scope and coverage

Reviewed commit `6f60fb8`, which fixes OX-0033, OX-0035, OX-0037, OX-0038, and
OX-0043. The review covered the prompt cache key in `openai.rs` and its caller
in `acp/prompt.rs`; `Capture` in `process.rs` and its users in `tools/shell.rs`
and `shell_processes.rs`, including background shell process reads; the
reasoning part separator in the OpenAI stream and in the terminal response
check; the `wait` tool and `describe` in `subagents.rs`; and the
`mark_temporary` rename. Lenses: correctness, testing, simplicity, and naming.

The live OpenAI request in the work log was not repeated, and prompt cache hit
rates were not measured.

## Findings

No confirmed findings.

## Checks run

- `make check` — passed. Twelve terminal tests were skipped because they require
  `make e2e`; terminal behavior did not change.

## Verdict

The five fixes do what their issues asked, and their tests cover the new
behavior. No open findings.
