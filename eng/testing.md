# Testing

Run `cargo test` for the in-module `#[cfg(test)]` suite and `cargo build` for a
debug build.

For live end-to-end testing, prefer
`scripts/run.py [--keep] [--repo <GitHub-commit-URL>] [--model <model-id>] [--effort <effort>] '<prompt>'`.
It reads `OPENROUTER_API_KEY` from `.env` and creates a temporary workspace. To
use an existing workspace, run
`ox run [--dir <workspace-path>] [--model <model-id>] [--effort <effort>] '<prompt>'`
directly. The model must be in the model catalog and the effort level must be
one it lists; an unset model or effort uses
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

Test the example careful skill's hook script with
`python3 -m unittest discover -s examples/skills/careful/scripts`. It uses a
temporary Git repository and needs no OpenRouter key. Install and invoke it the
same way as the goal skill.

## Test discipline

The test suite is curated code. Each test owns one durable, observable
guarantee, and the suite changes as the guarantees change.

- Give each guarantee one owning test, named for that guarantee. A test that
  checks unrelated guarantees is split so each failure names what broke.
- A change adds a test for each new guarantee and for each reproduced
  regression, at the closest stable boundary. It rewrites or deletes the tests
  for guarantees it changes or removes, in the same change.
- Test a guarantee once. Unit, orchestration, ACP, and end-to-end tests repeat an
  assertion only when those layers have distinct failure modes.
- Use table-driven cases for one behavior over varied inputs. Each case states
  its input and expected result, and a failure message identifies the case.
- Keep setup proportional to the guarantee. Shared fixtures and helpers hold
  setup that several tests need, so each test body reads as its guarantee.
- Test observable behavior. Internals change freely while the guarantees they
  serve hold.
- Before finishing any change that touches tests, inspect the complete test diff
  and report which guarantees gained, lost, or moved their owning tests.
