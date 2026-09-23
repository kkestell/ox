# Split search output collection and transcript validation

## Goal

`run` in `src/tools/search.rs` (cognitive score 36) and `validate_transcript`
in `src/sessions.rs` (score 33) each do several jobs inside one loop. Split
each into small functions with one job apiece, with no change in behavior: the
same search output, notices, and diagnostics, and the same transcripts
accepted or rejected with the same error text.

## Related code

- `src/tools/search.rs:84` — `run`: spawns ripgrep, reads each candidate name,
  checks it against the workspace, scans the file for grep or checks it is a
  regular file for glob, collects per-file diagnostics, kills ripgrep on
  truncation, then appends the truncation or no-match notice and the
  diagnostics section. The per-file error push appears twice, once for a failed
  open and once for a failed scan.
- `src/tools/search.rs:172`, `:182` — `append_match` and `scan_file`: already
  one job each. They stay as they are.
- `src/tools/search.rs:350` —
  `unreadable_paths_are_reported_alongside_the_matches_that_were_found`: covers
  only the ripgrep enumeration notice, not a per-file diagnostic.
- `src/tools/search.rs:367`, `:421` — tests that call `run` directly with a
  stand-in command. `run` keeps its signature.
- `src/sessions.rs:473` — `validate_transcript`: one `while` loop over an
  index with every rule written inline in one `match`: the opening model entry,
  no later model entry, the settings block, the current skill, hook feedback
  placement, compaction checkpoint prefixes, orphan tool results, and assistant
  batches.
- `src/sessions.rs:440` — `pair_results`: the existing pattern of one function
  per rule. The assistant batch check keeps calling it.
- `src/sessions.rs:1315` — `read_rejects_a_malformed_transcript` and
  `src/sessions.rs:1586` — `empty_transcripts_are_valid_and_settings_fold_in_order`:
  every rejection asserts only `is_err()`.

## Decisions

- **Search output becomes one struct.** A private `SearchOutput` holds
  `output`, `truncated`, and `local_errors`, the three values the loop mutates
  today. Its methods take the place of the nested branches, so each candidate
  is one call and the final formatting is one call. This follows the `Parser`
  cursor from `eng/plans/2026-09-23-005-patch-parse-and-model-loop-refactor.md`.
- **The candidate loop stops on truncation in its condition.** The loop reads
  `while !output.truncated && reader.read_until(..)`, and ripgrep is killed
  once after the loop when truncated. This matches today's kill-then-break.
- **Both per-file failures go through one `record_error`.** A failed open and a
  failed scan push the same `path: error` line under the same
  `DIAGNOSTICS_LIMIT` check, so they share one method.
- **Transcript validation keeps one loop and one `match`.** Each arm either
  returns an error or calls one check function, and the arm yields the next
  index. The loop keeps the state that crosses entries: the index, the current
  skill, the previous covered prefix, and the complete batch ends. A check that
  consumes several entries returns the index after them; any other check
  returns `io::Result<()>`.
- **Error text must not change.** Each error keeps its current text. Load
  failures show it inside `session {id} has an invalid transcript: …`.

## Naming

- `SearchOutput` — private struct in `src/tools/search.rs` for the search
  output being collected. Fields keep today's names: `output`, `truncated`,
  `local_errors`. Methods: `add_candidate` (check one ripgrep candidate and add
  its matches or name), `grep_file` (open and scan one file), `record_error`
  (one per-file diagnostic), and `finish` (the ripgrep failure check, the
  truncation or no-match notice, and the diagnostics section).
- `append_diagnostics` — free function in `src/tools/search.rs` that appends
  the `Some paths could not be searched:` section. Its `enumeration_failed`
  argument is whether ripgrep wrote anything to stderr.
- `settings_block_end` — checks one settings block and returns the index of
  the turn start after it. "Settings block" as used in `eng/architecture.md`.
- `check_hook_feedback_skill` — hook feedback with a skill name belongs to the
  current skill invocation.
- `check_hook_feedback_placement` — hook feedback follows the entry its hook
  kind requires.
- `check_compaction_checkpoint` — the summary is nonblank and the covered
  prefix advances, lies within the transcript so far, and ends at a complete
  assistant batch.
- `assistant_batch_end` — checks one assistant message and its tool results
  and returns the index after the batch.

"Transcript entry", "hook feedback", "hook kind", "compaction checkpoint",
"assistant batch", and "skill invocation" are as defined in
`eng/glossary.md`.

## Test plan

- Change each rejection in `read_rejects_a_malformed_transcript` and
  `empty_transcripts_are_valid_and_settings_fold_in_order` to assert the exact
  error text it produces today, recorded from the current code before any
  change. The misplaced hook feedback table gains the expected message as a
  third element. `store.read` cases assert that the error ends with the
  validation message. Each check then has a pinned owner, and no test
  function is added.
- Extend `unreadable_paths_are_reported_alongside_the_matches_that_were_found`
  with a file at mode `000` beside `a.rs`, and assert its `./name: ` diagnostic
  appears. This covers `record_error`, whose two copies this change merges. It
  is not meaningful when tests run as root, which the test already assumes for
  the locked directory.
- The other search tests cover glob and grep results, truncation of both,
  missing ripgrep, cancellation, and workspace checks on candidates. Run them
  unchanged.

## Implementation plan

1. Before changing any code, run `rsloc --items` and save its output as
   `eng/scratch/2026-09-23-006-baseline.txt`.
2. In `src/sessions.rs`, change the rejection assertions to exact error text
   that matches current output. In `src/tools/search.rs`, extend the
   unreadable paths test. Run both tests before touching the code under test.
3. In `src/tools/search.rs`, add `SearchOutput` with `record_error`,
   `grep_file`, `add_candidate`, and `finish`, and add `append_diagnostics`.
   Reduce `run` to spawning ripgrep, the candidate loop, the join with
   `drain_errors`, and `finish`.
4. Run `cargo test tools::search`.
5. In `src/sessions.rs`, add `settings_block_end`,
   `check_hook_feedback_skill`, `check_hook_feedback_placement`,
   `check_compaction_checkpoint`, and `assistant_batch_end` beside
   `pair_results`, moving each arm's body out of `validate_transcript`. Keep
   the comment that the scan already validated every assistant batch with the
   checkpoint check.
6. Run `cargo test sessions`, then the full `cargo test`.
7. Run `rsloc --items` again and save its output as
   `eng/scratch/2026-09-23-006-final.txt`. Add a Results section to this plan
   in the same form as the one in
   `eng/plans/2026-09-23-005-patch-parse-and-model-loop-refactor.md`: `run`
   against `run`, `SearchOutput` and its methods, and `append_diagnostics`;
   `validate_transcript` against it and its five check functions; and the Prod
   totals of `src/tools/search.rs`, `src/sessions.rs`, and the crate.
8. Mark both items done in `eng/todo.md` with a pointer to this plan.

## Results

Measured with `rsloc --items` before and after the change. The raw output is
in `eng/scratch/2026-09-23-006-baseline.txt` and
`eng/scratch/2026-09-23-006-final.txt`.

| Area | Prod before | Prod after | Highest Cog before | Highest Cog after |
| --- | ---: | ---: | ---: | ---: |
| `run` → `run`, `SearchOutput`, `append_diagnostics` | 85 | 114 | 36 | 8 |
| `validate_transcript` → it and its five check functions | 132 | 169 | 33 | 5 |

| File | Prod before | Prod after | Cog before | Cog after |
| --- | ---: | ---: | ---: | ---: |
| `src/tools/search.rs` | 204 | 233 | 60 | 45 |
| `src/sessions.rs` | 866 | 903 | 81 | 65 |
| Crate | 6276 | 6342 | — | — |

The test suite has the same 96 test functions. Test code in `src/sessions.rs`
grew by 56 lines because every transcript rejection now pins its exact error
text, and in `src/tools/search.rs` by 4 lines for the unreadable file that
covers per-file diagnostics.
