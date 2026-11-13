# Review: 2026-11-13-001-github-pr-feed

## Round 1

- Task: `2026-11-13-001-github-pr-feed`
- Independence: I did not write this implementation; the user identified the implementer as Terra.
- Reviewed change: `/Users/kyle/src/ox`, branch `main`, staged implementation against `8cc9ae064bebb26a374e56517f663142961eff93`; [plan](../plans/2026-11-13-001-github-pr-feed.md); [work log](../work/2026-11-13-001-github-pr-feed.md). No unstaged code changes. The three GitHub plans and this task's work log are workflow records.

### Verdict

accept: No blocking findings or plan findings. Acquisition, classification precedence, account isolation, snapshot retention, pagination, and cache invalidation agree with the plan and Hypercube reference at `9f854a8`. Live verification confirmed the lab's attention judgments and unchanged-conversation reuse.

### Notes

- `crates/ox-github/src/lib.rs:115`: `graphql` accepts arbitrary JSON variables but sends every value through `-f`, which [gh defines as a string field](https://cli.github.com/manual/gh_api). Numbers, booleans, arrays, objects, and null therefore lose their types. All current feed callers supply strings, so this does not block this implementation. Narrow the accepted contract or preserve JSON types before adding typed callers.

### Verification

- Reused: `make check-rust`, passed; evidence: [final log](../work/logs/2026-11-13-001-final-check-rust-2.log). Inspected actual fixtures/assertions and the logged negative tests for host selection and pagination validation.
- Reused: `make e2e`, passed all eight spec files; evidence: [e2e log](../work/logs/2026-11-13-001-e2e.log).
- Run: `git diff --cached --check`, passed.
- Run: `cargo run --offline --manifest-path /var/folders/x3/s0dy9w311sncwf87160crpw80000gn/T/ox-github-live-review-e3vf9rfp/Cargo.toml --target-dir /Users/kyle/src/ox/target -- /var/folders/x3/s0dy9w311sncwf87160crpw80000gn/T/ox-github-live-review-e3vf9rfp/bin`, passed. Needed to validate real GraphQL execution beyond canned CLI fixtures. The temporary harness includes the actual native feed module and transport, selects `kkestell`, and restricts search variables to `kkestell/hypercube-pr-lab`. All 17 scenarios matched: six Fix, two Replies, two Merge, one Review, four Waiting, two omitted. Both refreshes agreed; conversation requests dropped from one batch to zero. Evidence: `result.log`, `requests.jsonl`, and `lab-prs.json` beside the harness manifest.
- Limitations: live checks were read-only and repository-scoped; they did not exercise logout, account failures, pagination beyond the lab, or mutations that invalidate cached conversations. Those paths were inspected through fixture tests. No UI presentation exists in this unit.
