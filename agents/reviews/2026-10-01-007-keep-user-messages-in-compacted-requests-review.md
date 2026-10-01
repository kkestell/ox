# Keep user messages in compacted requests review

## Scope and coverage

Reviewed commit `da9bb49` against its plan and work log. Traced compacted
request projection, cut selection, prompt admission, provider encoding,
checkpoint persistence, subagent messages, images, and the prompt-loop callers.
Used the correctness, testing, performance, architecture, simplicity,
error-handling, Rust ownership, and Rust idioms lenses.

The test diff gives exact projection ownership to the retained-user-message and
action-log test, cut ownership to the recent-allowance table, skill ownership to
the latest-invocation table, and prompt admission ownership to the new oversized
user-message test. Existing image, subagent, automatic compaction, manual
compaction, and provider tests were strengthened. The retired smallest-request
ranking and per-entry byte-sum assertions described mechanisms removed by this
change; no observable guarantee moved or lost.

No live model check was run, so the review did not assess how consistently real
models follow the new five-heading summary prompt. Terminal end-to-end tests
were not run because the change does not alter terminal behavior.

## Findings

No confirmed findings.

## Checks run

- `git diff --check da9bb49^ da9bb49` — passed.
- `make check` — passed; the 12 tmux tests remained ignored as expected.

## Verdict

The commit implements the approved compaction design without a confirmed
correctness, maintainability, or performance defect. No follow-up fix is needed.
