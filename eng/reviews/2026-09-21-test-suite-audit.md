# Test suite value audit

Reviewed the complete Rust test corpus on 2026-09-21, including the four
uncommitted prompt-settings tests from the simplification pass. The scope was
all 90 test functions in the 13 Rust source files. The review judged whether
each test protects distinct observable behavior at a stable boundary, repeats
coverage already supplied elsewhere, or carries avoidable maintenance cost.
No separate integration-test directory or non-Rust test suite exists.

## Verdict

The suite is not half worthless. Seventy-five tests protect distinct behavior
at a reasonable boundary. Eight test functions should disappear through
deletion or consolidation, and seven retained tests should be pruned because
they repeat assertions owned by another layer.

The concern is nevertheless real. Test modules contain 4,115 lines while the
same files contain 4,172 other lines, including test-only fixtures that appear
before the test modules. `src/acp.rs` and
`src/acp/prompt.rs` account for 1,848 test-module lines against 969 production
lines. Most excess coverage is concentrated there: tool execution, transcript
persistence, model-request encoding, ACP replay, cancellation, and client
updates are repeatedly asserted in the same scenario.

| Module | Tests | Keep as-is | Remove or consolidate | Retain but prune |
| --- | ---: | ---: | ---: | ---: |
| `main` | 3 | 3 | 0 | 0 |
| `auth` | 2 | 2 | 0 | 0 |
| `openrouter` | 10 | 10 | 0 | 0 |
| `sessions` | 7 | 6 | 0 | 1 |
| `tools` | 5 | 4 | 0 | 1 |
| `tools::patch` | 8 | 8 | 0 | 0 |
| `tools::read` | 3 | 3 | 0 | 0 |
| `tools::search` | 5 | 5 | 0 | 0 |
| `tools::shell` | 8 | 7 | 0 | 1 |
| `acp::convert` | 3 | 3 | 0 | 0 |
| `acp::operations` | 3 | 3 | 0 | 0 |
| `acp::prompt` | 20 | 11 | 7 | 2 |
| `acp` | 13 | 10 | 1 | 2 |
| **Total** | **90** | **75** | **8** | **7** |

## Test functions to remove or consolidate

### Delete a constant-value test

`acp::tests::initialize_answers_with_the_supported_protocol_version` calls a
pure response constructor and checks the protocol-version constant passed by
that constructor. It protects no conditional behavior and would only fail if
the implementation and its adjacent constant were edited inconsistently.

### Delete four repeated prompt tool integrations

The following `acp::prompt` tests repeat guarantees already covered by the
tool modules, session encoding tests, the generic ordered-result prompt test,
and the full ACP permission or shutdown tests:

- `shell_failures_reach_the_next_model_request_and_replay`
- `running_shell_cancellation_is_saved_and_skips_later_calls`
- `shell_result_survives_update_failure_or_late_cancellation`
- `file_and_search_results_are_saved_and_sent_to_the_next_model_request`

The shell module owns exit, timeout, cancellation, and process cleanup. The
tool dispatcher owns each built-in name. The session and OpenRouter tests own
result persistence, replay conversion, and next-request encoding. The prompt
suite needs one successful ordered tool loop, one interrupted loop, and one
failed-update loop; it does not need to rerun those guarantees for every tool
or outcome type.

### Consolidate one patch interruption test

`cancelling_after_the_result_keeps_the_applied_patch` and
`an_update_failure_after_a_patch_still_saves_its_result` exercise the same
boundary: once an effectful patch has returned, a later interruption must not
erase the observed result. Keep one table-driven test with late cancellation
and failed-update cases instead of two functions.

### Fold two settings tests into existing turn tests

`empty_session_without_a_selection_uses_defaults` belongs in
`a_text_answer_is_saved_in_the_transcript`: run that existing basic prompt
without a selection and assert the default request there.

`saved_model_wins_over_selection_while_selected_effort_applies` belongs in
`different_efforts_are_saved_and_sent_for_sequential_turns`: give the second
turn a conflicting selected model while retaining its selected effort. The
existing test already owns saved-model and changed-effort resolution across
turns.

The one genuinely new settings regression is
`absent_selection_uses_saved_settings_without_duplicate_entries`. It directly
protects the redundant-read path removed by the simplification.

## Retained tests that should lose assertions

- `sessions::tests::a_saved_batch_can_be_replayed_and_sent_in_the_next_request`
  should retain database round-trip and transcript ordering assertions. ACP
  replay shape belongs to `acp::convert`; OpenRouter message shape belongs to
  `openrouter`.
- `tools::tests::patch_schema_tool_call_title_and_argument_errors` should stop
  checking the fallback title already covered by
  `tool_call_titles_describe_the_call_and_fall_back_to_the_tool_name`.
- `tools::shell::tests::arguments_schema_and_tool_call_title` should likewise
  leave title behavior to the common title test.
- `acp::prompt::tests::a_patch_call_writes_its_files_and_saves_its_summary`
  should retain the applied files and saved result, but drop schema presence,
  replay rendering, and next-request assertions owned elsewhere.
- The retained combined patch-interruption test should assert the applied
  effect and stored observed result, not replay formatting again.
- `acp::tests::shell_permissions_control_execution_and_save_results` should
  validate permission-request shape and operation-guard behavior in one
  representative case. Its decision table should assert only the differing
  execution, result, and response behavior for the other seven cases.
- `acp::tests::listing_rejects_cursors_and_workspaces_must_be_absolute` should
  retain cursor rejection. Relative-path rejection and idempotent deletion are
  already covered by the session-store tests and other handler tests.

## Coverage worth retaining

The remaining tests are not padding. In particular, the apply-patch parser and
filesystem tests cover destructive boundary behavior; shell tests cover real
process-group cleanup and bounded output; OpenRouter tests cover fragmented
stream assembly and opaque continuation metadata; operation tests cover guard
and shutdown concurrency; session tests cover transactional transcript
validation; and the ACP transport tests cover EOF, SIGINT, permission, and
cleanup ordering that lower-level tests cannot establish.

## Recommended cleanup result

Applying the removals and consolidations would reduce the suite from 90 to 82
test functions without dropping a distinct guarantee. Pruning the seven
retained tests should remove substantial duplicated setup and assertions as
well. The exact line reduction should be measured from the cleanup diff rather
than estimated in advance.

No production or test code was changed during this audit. The only other
repository change was the test-discipline guidance added to `AGENTS.md`.
