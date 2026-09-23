# Testing

Run `cargo test` for the in-module `#[cfg(test)]` suite and `cargo build` for a
debug build.

For live end-to-end testing, prefer
`scripts/run.py [--keep] [--repo <GitHub-commit-URL>] [--model <model-id>] [--effort <default|low|medium|high>] '<prompt>'`.
It reads `OPENROUTER_API_KEY` from `.env` and creates a temporary workspace. To
use an existing workspace, run
`ox run [--dir <workspace-path>] [--model <model-id>] [--effort <default|low|medium|high>] '<prompt>'`
directly. The model must be in the model catalog; an unset model or effort uses
the same defaults as a new ACP session. The headless run creates a new session
in `ox.db`, prints the final answer to stdout when the prompt ends normally, and
otherwise prints nothing to stdout and exits with a failure status. Set
`OX_DATA_DIR` to a temporary directory to isolate that database, then inspect it
to verify the saved session.

Test the example goal skill's hook script with
`python3 -m unittest discover -s examples/skills/goal/scripts`. It uses a fake
`ox` executable and needs no OpenRouter key. To try the skill live, copy
`examples/skills/goal` into a workspace's `.agents/skills/goal` and invoke
`/goal <objective>` from an ACP client; headless runs never invoke skills.

## Test discipline

- The test suite is curated code, not an append-only log of changes. Feature
  work, bug fixes, refactors, and hardening all have the same obligation to
  avoid permanent test growth.
- Default to adding no new test function. Before writing one, inspect the
  relevant existing tests and extend, replace, merge, or delete them so the
  changed behavior is covered at the closest stable boundary.
- A new test is justified only by a distinct, durable, observable guarantee or a
  reproduced regression that existing coverage cannot express clearly. New code,
  another branch, another input row, or a plan item is not by itself a reason
  for another test.
- When a new test is justified, look for stale, overlapping, or lower-value
  tests in the same area and remove or consolidate them. The default test-count
  and test-code budget for every change is flat or lower, not just for
  simplifications.
- Give each guarantee one owner. Do not repeat the same assertions in unit,
  orchestration, ACP, and end-to-end tests unless those layers have distinct
  failure modes. Table-driven cases exercising one behavior belong in one test.
- Do not game the count by combining functions while retaining duplicated setup
  and assertions. Optimize the maintenance surface, not the reported number of
  tests.
- Before finishing any change that touches tests, inspect the complete test diff
  and report the test-function and test-code delta. Any net growth needs a
  concrete explanation of the previously unprotected behavior.
