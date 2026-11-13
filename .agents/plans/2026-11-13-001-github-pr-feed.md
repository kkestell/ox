# Read and classify the signed-in accounts’ pull requests

- Task: `2026-11-13-001-github-pr-feed`
- Kind: plan
- Status: ready
- Source HEAD: `8cc9ae064bebb26a374e56517f663142961eff93`
- Dependencies: none

## Outcome and boundaries

Supply ox’s desktop UI with github.com PRs across signed-in accounts and repositories, preserving Hypercube’s five groups and conversation-based attention rules. No workspace is required. This unit supplies the native feed; 002 adds Chat and 003 presents both. No Review action, daemon inbox, repository polling, credential discovery through push permissions, or persistent synchronization store.

## Current system

`crates/ox/src/lib.rs` owns desktop state and Tauri commands; `oxd` owns ordinary sessions/worktrees. Hypercube’s reference is locally available commit `9f854a8`: `internal/github/{gh,graphql,pulls}.go`, `watcher_test.go`, and `internal/ui/web/github/turns{,.test}.ts`. Its conversation-cache change is `d3fa6c8`; the recorded previous cost was approximately 1,900 GraphQL points/account/hour.

## Design

Create `crates/ox/src/github/` with GraphQL acquisition/cache, serializable domain types, and pure classification modules. Put CLI transport and account-token resolution in `crates/ox-github`, shared with 002’s daemon launch path; no provider framework. Keep `lib.rs` as wiring. `App` owns the in-memory feed behind an async mutex; `github_refresh` returns a snapshot, never credentials. Release that mutex before Chat/session operations. Unit 003’s hook prevents overlapping refreshes.

Discover accounts using `gh auth status --hostname github.com --json hosts`, removing inherited `GH_TOKEN`/`GITHUB_TOKEN` for account discovery and token selection, as Hypercube does. Obtain each token using `gh auth token --hostname github.com --user LOGIN`; scope it to that account’s `gh api graphql` children. Reacquire on every refresh/action, never cache tokens or switch the active login. Execute argument arrays, not shell command strings; collect stderr and reject CLI/GraphQL/decoding errors. Child lifetimes belong to the operation; aborting it reaps its children. Enable required Tokio process/I/O features; add test-only `tempfile`.

Preserve Hypercube’s involved, requested, and direct-request searches, deduplication, team/direct distinction, required-check failure lookup, and per-account interpretation. Follow search cursors to GitHub’s search ceiling; report remaining/truncated results instead of claiming completeness. Keep the existing ten-opinion and twenty-conversation-comment windows and committed-time approximation; paginate review threads/check contexts. Do not import Hypercube’s test-repository exclusion.

Read summaries first. Reuse per-account conversation entries keyed by PR ID, `updatedAt`, and review-thread resolution state; read all resolution pages when present so later threads invalidate correctly. Batch changed conversations; prune entries absent from a successful refresh. Failed reads preserve that account’s previous complete snapshot and timestamp, with an error. Account discovery failure retains the preceding accounts with a top-level error; successful discovery removes signed-out accounts. Publish successful accounts independently of failing ones.

Classification takes explicit author/viewer facts, including separate “changes requested on current head” and “author updated since my review” fields; no overloaded `responded` flag. Return one group/reason/rank decision, or omission. Preserve Hypercube’s precedence, bot/deleted-author handling, requested-review priority, draft/wait behavior, and ready-to-merge interpretation. UI maps reason codes to text/icons only.

Snapshot: top-level discovery error and sorted accounts; each account has login, last-success timestamp, error, truncation indication, and PRs. Each PR carries canonical identity/URL, title, author, head branch, update time, and its classification. Row identity is `(account, repository, number)`; the same PR may appear for two accounts. Sort within groups by existing reason rank, then account order and update order. Attention counts exclude Waiting.

## Check your assumptions

1. **Confirmed: preserve all five groups and their attention judgments.** The user explicitly chose this over simpler factual groups; optimization must retain those semantics.
2. **Kept: github.com and installed, signed-in `gh` are prerequisites.** This matches Hypercube’s supported host; Enterprise and an in-app login flow are optional later scope.
3. **Kept: history remains an inference, not proof that a push answered a comment.** Hypercube uses commit time and bounded comment/opinion windows; retain and document those limits rather than silently promise stronger judgments.

## Implementation sequence

1. Add `ox-github` transport, desktop domain modules, account discovery, snapshot/error handling, and `github_refresh` registration in `lib.rs`.
2. Add summary/conversation/check queries and memory-cache invalidation.
3. Port the reference behavioral cases into pure Rust classification tests and connect classification to snapshots.

## Verification

- Rust tests beside the modules use a fixture `gh` executable via per-command PATH, never process-wide environment mutation or real credentials.
- Cover multiple accounts, both interpretations of one PR, requested PRs outside involved results, pagination/truncation, all reference precedence cases, bots/deleted authors, pushes/replies/resolution, required versus optional failures, errors retaining data, and logout.
- Assert unchanged conversations cause no detail requests; updated time or a resolution beyond the first page causes rereading. Assert scoped account selection and absence of token material from returned data/errors.
- Command: `make check-rust`. Full app checks run with 003.
- Sources: [gh API](https://cli.github.com/manual/gh_api), [account discovery](https://cli.github.com/manual/gh_auth_status), [account tokens](https://cli.github.com/manual/gh_auth_token).
