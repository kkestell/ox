# Split patch parsing and the model loop

## Goal

`Patch::parse` in `src/tools/patch.rs` (cognitive score 51) and
`PromptRun::run_model_loop` in `src/acp/prompt.rs` (score 34) are the two
hardest functions in the codebase to read. Split each into small functions
with one job apiece, with no change in behavior: the same patch errors at the
same line numbers, and the same prompt outcomes, commits, updates, and hook
runs in the same order.

## Related code

- `src/tools/patch.rs:33` — `Patch::parse`: one loop that reads the begin
  marker, each file operation header, `Add File` body lines, the optional
  `Move to`, each chunk with its anchor and body lines, and the end marker,
  tracking one shared line index throughout.
- `src/tools/patch.rs:560` — `malformed_patches_report_lines_and_never_write`:
  checks only that each malformed input fails with a `parse: line` prefix.
- `src/acp/prompt.rs:325` — `run_model_loop`: makes a model request, sends
  pending tool calls, runs tools, commits, sends the usage update, then either
  runs `after_tools`, runs `before_stop` and counts a hook continuation, or
  stops. Every fallible step is spelled out as `if let Err … return`.
- `src/acp/prompt.rs:469`, `:503`, `:535` — `run_before_tool`,
  `run_after_tools`, `run_after_run`: the existing pattern of one method per
  hook kind, which `before_stop` will follow.
- `src/acp/prompt.rs:596` — `save_stop_feedback`: enforces
  `MAX_HOOK_CONTINUATIONS`. It stays as is.

## Decisions

- **The patch parser becomes a cursor over the lines.** A private `Parser`
  struct holds the lines and the current index, with one method per part of
  the patch format. This removes the nested `while` loops and the manual
  `index += 1` scattered through every branch. A separate tokenizer or state
  enum would add more code than the format needs.
- **Error text and line numbers must not change.** The model reads these
  errors to fix its patch. Each error keeps its current text and reports the
  same line as today, including the `index + 1` offset for text after
  `*** End Patch` and the header line for an empty file path.
- **The model loop makes one model request per call to a helper.**
  `run_model_step` returns
  `Result<ControlFlow<PromptOutcome>, PromptOutcome>`. `Err` is a failure that
  stops the prompt run, `Break` is a normal stop (finished, token limit,
  refused), and `Continue` means another model request. This lets every
  fallible step use `?`, and `run_model_loop` shrinks to a loop that matches on
  the result. `std::ops::ControlFlow` is in the standard library, so this adds
  no dependency.
- **Ordering inside a model step is unchanged.** Tools run only on a tool-calls
  stop, and a tool failure returns before `commit`, as it does today. `commit`
  and `send_usage` then run on every completion, and only after that does the
  stop choose what comes next. The intermediate `Option<PromptOutcome>` goes
  away because the stop is matched directly after the commit.
- **`before_stop` moves into `run_before_stop`**, next to the other hook-kind
  methods. It records the final answer, runs every `before_stop` hook, counts a
  hook continuation when any hook returns `continue`, and returns `Continue` or
  `Break(PromptOutcome::Finished)`.

## Naming

- `Parser` — private cursor over a patch's lines in `src/tools/patch.rs`.
  Methods: `parse` (the whole patch), `file_operation` (one header and its
  body), `add_file`, `update_file`, `chunk`, `error`. `FileOperation`,
  `Operation`, `Chunk`, and chunk `anchor` keep their existing meanings.
- `run_model_step` — one model request in a prompt run, through its commit and
  the hooks that follow it. "Model request" as defined in `eng/glossary.md`.
- `run_before_stop` — runs the `before_stop` hooks on a finished answer.
  "Hook continuation" and "stop decision" as defined in `eng/glossary.md`.

## Test plan

- Change `malformed_patches_report_lines_and_never_write` so each case pairs
  its input with the exact error it expects today. Record those strings from
  the current code before changing the parser. This test function then pins
  every parse error and its line number, and no test function is added.
- The existing `acp::prompt::tests` already cover finished answers, tool
  batches, `before_stop` continue, stop, and failure, the hook continuation
  limit, cancellation before and during tools, update failures, and failed
  batch saves. Run them unchanged. Add no prompt tests.

## Implementation plan

1. Before changing any code, run `rsloc --items` and save its output as
   `eng/scratch/2026-09-23-005-baseline.txt`.
2. In `src/tools/patch.rs`, turn the malformed-patch table into
   `(input, expected error)` pairs that match current output. Run the test
   before touching the parser.
3. Add `Parser` with `error(index, reason)`, then move `Add File` body reading
   into `add_file`, chunk reading into `chunk`, and `Move to` plus the chunk
   loop into `update_file`. `file_operation` dispatches on the header and
   checks for an empty path. `Patch::parse` builds a `Parser` and returns its
   `parse` result.
4. Run `cargo test tools::patch`.
5. In `src/acp/prompt.rs`, add `run_before_stop(&mut self, answer: String)`
   from the `Finished` arm of `run_model_loop`.
6. Add `run_model_step` holding the loop body, using `?` for each fallible
   step and matching on `openrouter::Stop` after `send_usage`. Reduce
   `run_model_loop` to the loop that calls it.
7. Run `cargo test acp::prompt`, then the full `cargo test`.
8. Run `rsloc --items` again and save its output as
   `eng/scratch/2026-09-23-005-final.txt`. Add a Results section to this plan
   comparing baseline and final metrics. Match items by name, since line
   numbers shift. For each area, sum Prod and take the highest Cog of the
   original function against those of the functions that replace it:
   `Patch::parse` against `Parser` and its methods, and `run_model_loop`
   against `run_model_loop`, `run_model_step`, and `run_before_stop`. Also
   compare the Prod totals of `src/tools/patch.rs`, `src/acp/prompt.rs`, and
   the whole crate.

## Results

Measured with `rsloc --items` before and after the change. The raw output is
in `eng/scratch/2026-09-23-005-baseline.txt` and
`eng/scratch/2026-09-23-005-final.txt`.

| Area | Prod before | Prod after | Highest Cog before | Highest Cog after |
| --- | ---: | ---: | ---: | ---: |
| `Patch::parse` → `Patch::parse`, `Parser` | 101 | 127 | 51 | 7 |
| `run_model_loop` → `run_model_loop`, `run_model_step`, `run_before_stop` | 64 | 59 | 34 | 4 |

| File | Prod before | Prod after | Cog before | Cog after |
| --- | ---: | ---: | ---: | ---: |
| `src/tools/patch.rs` | 385 | 411 | 104 | 79 |
| `src/acp/prompt.rs` | 837 | 832 | 130 | 107 |
| Crate | 6255 | 6276 | — | — |

The test suite has the same 96 test functions. Test code in
`src/tools/patch.rs` grew by 27 lines because
`malformed_patches_report_lines_and_never_write` now pins each exact error
message and line number, and it gained one case for an empty file path.
