# Start an ordinary chat at a pull request’s head commit

- Task: `2026-11-13-002-github-pr-chat`
- Kind: plan
- Status: ready
- Source HEAD: `8cc9ae064bebb26a374e56517f663142961eff93`
- Dependencies: `2026-11-13-001-github-pr-feed`

## Outcome and boundaries

Chat opens an ordinary Claude session at the PR head, bound to the row’s GitHub account through every agent restart. Reuse a matching workspace or add/clone its repository. Never send a prompt automatically. Unit 003 supplies UI/composer seeding. No Review, force-reset, daemon PR feed, workspace-wide account switching, or new session kind.

## Current system

`Client::new_session` accepts a commit as a new worktree’s start; `oxd` creates `ox/<id>`. `Session::run` launches agents for creation/load/unarchive. `Store::add_session` is transactional; optional session tables use cascading foreign keys. Hypercube’s `internal/github/chat.go` and `internal/workspace/remote.go` at `9f854a8` provide the acquisition reference.

## Design

Desktop `github/chat.rs` owns acquisition/orchestration; `oxd` owns durable session identity and launch authentication. Both use 001’s `ox-github` transport; no duplicate resolvers or provider framework. Acquisition needs no daemon fetch API.

Add optional `github_account` to `session.new`, `Client::new_session`, `SessionInfo`, `SavedSession`, and `Session`; update every caller/type mirror. Ordinary sessions use None; Chat supplies the row login. `oxctl new --github-account LOGIN` exposes the same account selection. Persist only login in `session_github_accounts(session_id PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE, login NOT NULL)`, inserted in the session transaction and read on restore. Creating this optional table requires neither deleting existing sessions nor a migration framework. Protocol stays at 1.

At every `Session::run`, resolve the saved login through `gh auth token --hostname github.com --user LOGIN`, using the caller’s launch environment/PATH but removing inherited token overrides for lookup. Override both `GH_TOKEN` and `GITHUB_TOKEN` for the agent and its children. Never persist tokens or fall back to another account. Lookup failure records the ordinary agent-start failure; preserve identity/history for a later explicit load. Sessions without a binding retain existing behavior. Resolution belongs here, not in desktop load handlers, so CLI load, unarchive, parking and daemon restart all obey it. This binds GitHub token authentication, without changing SSH identities or user Git configuration.

`github_chat` accepts a cached row key and optional existing/clone directory; return `needs_repository { repository }` or `started { session, draft, warning? }`. Reacquire the row account’s token and verify the PR remains open. Obtain canonical repository/PR metadata from GitHub, never a webview-supplied URL/token.

Choose the first workspace with a configured remote matching the base repository case-insensitively, recognizing HTTPS/SSH/scp URLs before transport rewrites. Otherwise return `needs_repository`. Validate an explicit existing checkout before registering/reusing its path. Clone only into an absent absolute target using the canonical HTTPS URL, then register it. Preserve/report completed clone/workspace paths if subsequent creation fails; never recursively delete user directories.

Authenticate clone/fetch using scoped tokens and process-only HTTPS credential-helper reset plus `!gh auth git-credential`. Clear repository-local Git variables as `worktree::Git` does. Never change stored credentials/settings or the active login. Avoid `gh pr checkout`: existing checkouts and daemon worktree ownership must remain intact.

Fetch `refs/pull/NUMBER/head` into unique `refs/ox/fetch/UUID` with `--no-write-fetch-head --no-tags`. Keep the ref until ordinary session creation pins its new branch, then delete it. Forks use the base repository’s PR ref. Cleanup warnings retain created-session identity. Share ordinary Claude launch wiring and return title/URL as an unsent draft. No retries or branch-name probing.

## Check your assumptions

1. **Kept: fresh `ox/<id>` branch.** This avoids branch collisions; pushing back requires an explicit destination.
2. **Kept: start Claude immediately, prompt unsent.** Reuse ordinary session creation.
3. **Changed by user: the row’s account belongs to the session.** Preserve the login and resolve fresh credentials on every launch; acquisition-only authentication was rejected as incorrect multi-account behavior.
4. **Kept: offer add/clone for missing repositories.** Preserve Hypercube’s reach.

## Implementation sequence

1. Extend protocol/client/CLI, store and session identity; resolve bound credentials in `Session::run`. Update all callers and tests.
2. Add desktop acquisition, temporary-ref cleanup, shared session-start wiring and `github_chat`.
3. Add native/daemon tests before 003’s UI integration.

## Verification

- Native tests use real local Git repositories and fixture `gh` via per-command PATH/URL rewrites. Cover matching, clone collisions/preservation, closed/fork PRs, fetch failure, unchanged checkout/FETCH_HEAD, ref lifetime and cleanup warnings retaining session identity.
- `crates/oxd/tests/daemon.rs`: bound account overrides conflicting ambient tokens on create, park/load, unarchive and daemon restart; changed fixture tokens prove fresh lookup. Missing credentials must fail without fallback; concurrent accounts remain isolated. Store tests cover identity persistence/cascade and opening an existing database. Inspect agent environment through a fixture agent, not returned/logged tokens.
- Commands: `make check-rust`, `make check-ui`; 003 runs final E2E and required `make smoke-claude` because daemon/protocol/CLI now change.
- Sources: [Git credential helper implementation](https://github.com/cli/cli/blob/trunk/pkg/cmd/auth/gitcredential/helper.go), [credential environment](https://cli.github.com/manual/gh_help_environment).
