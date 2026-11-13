# Slash-command discovery and completion

- Task: `2026-11-12-015-slash-command-completion`
- Status: complete
- Plan: `.agents/plans/2026-11-12-015-slash-command-completion.md`
- Review: pending

The user requested committing the verified implementation directly. Independent
review has not been performed.

## Changes

Implemented the accepted plan across the bridge, daemon, protocol, and composer.
Native initialization obtains commands before the first prompt; native command
events replace the catalog. Session snapshots carry the current commands. The
editor completes the leading token with shared menu styling, preserving the
ordinary submission path.

Changed paths:

- `crates/claude-code-acp/src/{bridge,commands,main,runner,updates}.rs` and
  `crates/claude-code-acp/tests/protocol.rs`
- `crates/ox-protocol/src/messages.rs`
- `crates/oxd/src/{session,state}.rs`, `crates/oxd/src/bin/oxd-fake-agent.rs`,
  `crates/oxd/tests/daemon.rs`
- `ui/src/{CommandInput,Composer,ModelPicker}.tsx`, `ui/src/protocol.ts`,
  `ui/src/styles.css`, `ui/e2e/specs/commands.e2e.ts`
- `scripts/smoke-acp.py`, `docs/acp-v2.md`, `docs/architecture.md`,
  `crates/claude-code-acp/README.md`

## Deviations

None. Failed initialization is reported during session opening, as accepted.

## Verification

| Command | Result | Evidence |
| --- | --- | --- |
| `make fix` | Passed | Formatter output in builder session |
| `git diff --check` | Passed | Builder session |
| `make check` | Failed: formatting and Clippy passed; 23 bridge unit tests and 55/56 protocol tests passed. The new catalog test's shell fixture exited 22 when matching the prompt's exact JSON serialization. Remaining workspace suites, UI and script checks did not run. | `/tmp/ox-015-check.log` |
| `make e2e` | Not run after required-check failure | Pending |
| `make smoke-claude` | Not run after required-check failure | Pending |

## Limitations

The new bridge test must parse captured prompt JSON rather than depend on key
ordering. Required checks, removal-of-fix regression proofs, and visual menu
inspection remain incomplete. No staging or commits. Returned to the
coordinator at the required-check failure as instructed.

## Repair round 1

Parsed captured JSON; updated the rejection test for eager startup and an
acknowledged input barrier; corrected WebDriver text, whitespace and type
assertions. Simplified daemon setup ownership. The composer and popup now share
a borderless positioning region. Final screenshots match the model-menu styling
and align the popup with the entry. All initial verification limitations are
resolved. No production workaround was added; Cargo checks ran serially after
concurrent builds interfered with doctest artifacts.

| Command | Result | Evidence |
| --- | --- | --- |
| `make check` | Intermediate failure: old test assumed rejected prompts meant no native process. Corrected to verify no prompt reached the initialized process. | `/tmp/ox-015-check-repaired.log` |
| `make check` | Intermediate failure: doctest could not resolve Tauri during concurrent e2e builds. | `/tmp/ox-015-check-final.log` |
| `make e2e` | Initial failure: test text/whitespace assertions. | `/tmp/ox-015-e2e.log` |
| `make check-ui` | Initial failure: potentially undefined indexed test element. Replaced with direct element lookup. | `/tmp/ox-015-check-ui.log` |
| `python3 /tmp/ox-015-proofs.py` | Both regressions failed as expected with implementation removed; sources restored. | `/tmp/ox-015-proof-bridge.log`, `/tmp/ox-015-proof-daemon.log` |
| `make check` | Passed: formatting, Clippy, 265 Rust tests, doctests, Biome, TypeScript and Ruff. | `/tmp/ox-015-check-serial.log` |
| `make e2e` | Passed: 40 tests, seven suites, including five command cases; final layout inspected. | `/tmp/ox-015-e2e-final.log`; screenshots in `/var/folders/x3/s0dy9w311sncwf87160crpw80000gn/T/ox-e2e-gji8Gw/` |
| `make smoke-claude` | Passed with configured Haiku, including discovery before prompts, native skill arguments and local command completion. | `/tmp/ox-015-smoke.log`, `/private/tmp/ox-acp-5dl7yd6h/` |
| `git diff --check` | Passed; nothing staged or committed. | Builder session |
