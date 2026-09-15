# General review: uncommitted changes

- **Scope:** all staged, unstaged, and untracked changes in the worktree
- **Mode:** general
- **Topics selected:** correctness, testing, readability, architecture, concurrency, security, dependencies, and documentation. The changes alter the browser/client build and UI, add a cross-runtime session-lock protocol signal, and update shared ACP-facing documentation. Performance and unsafe were not materially implicated.

## Findings

### P1 — The client check cannot pass

- **Path:** `client/src/components/session-information.tsx:60`
- **Consequence:** `bun run check` fails with `TS18048: 'primary' is possibly 'undefined'`. The new client therefore does not pass its declared TypeScript/build gate, and CI or a production build cannot complete.
- **Suggested fix:** Preserve the narrowing inside the callback, for example capture `const primaryID = primary.id` after the early return and call `setView(primaryID)`, or use a callback that re-reads the already-narrowed value in a way TypeScript accepts. Re-run `bun run check` afterward.

## Topic results

- **Correctness:** One confirmed build-blocking type error. The session-lock projection and lock probe paths were inspected; the focused Go tests passed.
- **Testing:** The added client unit coverage passed, but the client check failed as above. The new Playwright coverage could not be executed because the client does not type-check/build successfully.
- **Readability:** No additional confirmed finding.
- **Architecture:** The browser/ACP lock metadata boundary is consistent with the updated architecture and specification; no additional finding.
- **Concurrency:** `probeLock` uses a nonblocking lock probe and releases only a lock it acquired; the affected Go tests under the race detector passed. No additional finding.
- **Security:** Resource links are restricted to HTTP(S) before becoming browser links, and the existing client-side bounds remain applied. No additional finding.
- **Dependencies:** The package/lockfile changes are internally consistent enough for the client unit suite and bundling step to run; no additional dependency finding.
- **Documentation:** The specification and architecture updates describe the new advisory lock metadata and client behavior; no additional finding.

## Checks run

- `git diff --check` — passed.
- `go test -race -count=1 ./internal/agent ./internal/acp` — passed.
- `cd client && bun run test` — passed: 95 tests, 0 failures.
- `cd client && bun run check` — failed at TypeScript checking with the P1 error above (the Tailwind and browser bundle steps completed).
- A direct `bun test` invocation was also attempted, but it incorrectly included `client/e2e/smoke.spec.ts` and produced Playwright's “test() to be called here” harness error; the declared `bun run test` script excludes that directory and passed.

## Verdict

Fix the TypeScript error before treating the client changes as complete. The remaining selected topics have no confirmed findings from this review, subject to rerunning the client build and Playwright suite after the fix.
