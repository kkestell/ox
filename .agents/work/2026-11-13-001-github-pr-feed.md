# GitHub PR feed

- Task: `2026-11-13-001-github-pr-feed`
- Status: implemented
- Plan: `.agents/plans/2026-11-13-001-github-pr-feed.md`

## Changes

- `crates/ox-github/` discovers GitHub accounts, resolves fresh account tokens, and runs account-scoped GraphQL queries.
- `crates/ox/src/github/` acquires and classifies PRs, paginates searches, threads and checks, caches conversations, and retains the last successful account snapshot on errors.
- `crates/ox/src/lib.rs` registers `github_refresh`; Cargo files declare the dependencies.

## Deviations

None.

## Verification

| Command | Result | Evidence |
| --- | --- | --- |
| `GH_TOKEN=fixture-ambient GITHUB_TOKEN=fixture-ambient cargo test -p ox-github` | Passed | `/tmp/ox-gh-env-test.log` |
| `make check-rust` | Passed | `.agents/work/logs/2026-11-13-001-final-check-rust-2.log` |
| `cargo test -p ox --lib pagination_metadata_is_required` with pagination validation removed | Failed as expected | `.agents/work/logs/2026-11-13-001-negative-tests.log` |
| `cargo test -p ox-github account_access_ignores_ambient_tokens_and_scopes_graphql` with host selection removed | Failed as expected | `.agents/work/logs/2026-11-13-001-negative-tests.log` |
| `make e2e` | Passed, 8 spec files | `.agents/work/logs/2026-11-13-001-e2e.log` |

## Limitations

Conversation judgments use bounded comment and review windows and approximate pushes by commit time, as described in the plan. Verification used CLI fixtures, not a live GitHub account. UI presentation and Chat are separate follow-on plans.
