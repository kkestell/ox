# Multi-provider models

## Plan

`agents/plans/2026-10-01-002-multi-provider-models.md`

## Summary

The plan's goal is met. One server discovers every credentialed provider when it
starts and offers their models through qualified IDs. Each captured turn selects
its own client, and changing providers retains shared transcript content without
reusing provider-private continuation data.

## Decisions

- Catalog and client discovery is fixed at startup. Credential changes take
  effect after restart.
- Qualified IDs identify models outside provider requests. Provider requests
  continue to use their native model IDs.
- A compacted transcript suffix inherits the source turn's provider, including
  when its turn start is outside the suffix.

## Automated checks

- `make format` — passed.
- `git diff --check` — passed.
- `cargo test -p ox-server` — passed: 204 tests.
- `cargo test -p ox` — passed: 69 unit tests and the one non-tmux integration
  test.
- `make e2e` — passed: 12 isolated terminal tests.
- `make check` — passed.

The complete test diff was inspected. New tests own qualified lookup of equal
native IDs, credentialed catalog discovery and failure handling, qualified
settings and session validation, ACP provider metadata and client routing,
cross-provider continuation projection and compaction-suffix provenance, and TUI
provider-column layout. Existing request, transcript, and picker tests were
updated to make qualified IDs and provider-native HTTP IDs their respective
expectations. No guarantees were removed; changed guarantees moved to their
qualified-ID or provider-aware owning tests.

## Manual verification

Live provider checks were not run. They require authenticated external provider
accounts and network requests, which this work did not perform.
