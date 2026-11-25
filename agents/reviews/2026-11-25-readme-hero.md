# README hero screenshots

## Scope and coverage

The uncommitted changes in the `readme-hero` worktree, on base `5dbd0b3` (#100, Changes tab), against `agents/plans/2026-11-25-readme-hero.md` and the user's answers in its "Check your assumptions" section. The working tree held the plan, the fake refactor and scene player, the tagged `oxd` files, the e2e fixture changes and driver, the scene and fixture repos, the four images in `docs/images/`, and the README and doc edits. All four images were opened and checked.

The user asked that the change not needlessly complicate the code. Each addition to a production path or shared fixture was weighed:

- `internal/fake/fake.go`: `steps` and `then` replace `reply`, `thoughts`, and `pieces`, so the echo mode loses two fields and one branch of `tick`. The scripted mode reuses the tracker, approvals, configuration, and timers instead of copying them into a second agent type. It earns its place.
- `cmd/oxd`: one call in `main` and two small tagged files. Normal builds carry no flag and no path to the scene player.
- `services.ts`: `exchange` replaces `callTool`'s private reader, and `request` and the `scene` argument are one line each. `ui.ts`: the `scene` option and the hoisted `gitEnv`. `global-setup.ts`: one conditional tag. Nothing simpler would let the driver create sessions and children over the socket.

Gaps:

- No `make e2e` or `make screenshots` run by the reviewer; both passed in the implementer's run on this tree, and the images match the scene.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                   |
| -- | -------- | ------------- | -------- | ------ | ------------------------------------------------------- |
| F1 | Low      | Construction  | Fix      | Open   | Scripted-mode fields sit loose on every fake agent      |
| F2 | Low      | Documentation | Fix      | Open   | "Five test prompts" above a six-row table               |
| F3 | Low      | Correctness   | Rejected | —      | Desktop header shows a test-scaffolding worktree path   |
| F4 | Low      | Construction  | Rejected | —      | Scene turns keep their `end` string past `LoadScene`    |

## F1. Scripted-mode fields sit loose on every fake agent

Low · Construction · Fix · `internal/fake/fake.go:32`

`scene`, `session`, and `next` are valid only together and only in scripted mode, but they are three separate fields on every `Agent`. An echo agent carries a nil `scene` beside a nil `session` and a zero `next`, and a `session` without a `scene` is representable. `AGENTS.md` ("Correct by construction") asks for fields valid only together to live in one value that exists only while they are valid.

### Evidence

`begin` branches on `a.scene != nil`; `play` reads `a.scene.sessions`, `a.session`, and `a.next`. Nothing else touches them.

### Action

Replace the three fields with one `script *script` field, where `script` holds `scene *Scene`, `session *sceneSession`, and `next int`. `Scripted`'s `Start` sets `a.script = &script{scene: scene}`; `begin` branches on `a.script != nil`; `play` reads the fields through it. No new test: the existing `TestScene*` tests cover the behavior unchanged.

## F2. "Five test prompts" above a six-row table

Low · Documentation · Fix · `docs/architecture.md:380`

"The Fake agent" says it "Recognizes five test prompts:" and the table now lists six (`approve`, `background`, `wait`, `think`, `usage`, `usage reset`). The count is a second copy of a fact the table already states.

### Evidence

Read the section after the change.

### Action

Drop the count: "Recognizes these test prompts:". Run `make docs`.

## F3. Desktop header shows a test-scaffolding worktree path

Low · Correctness · Rejected · `cmd/ox/web/e2e/services.ts:78`

The desktop hero's header reads `/private/tmp/ox-e2e-YhCaWN/worktrees/TBTkoPzdZMq`. The worktree root comes from the services fixture's `ox-e2e-` temporary directory, and `oxd` resolves its symlinks (`internal/server/worktree.go:94`), so `TMPDIR=/tmp` does not shorten it on macOS. A reader of the README sees a test path where a user would see `~/.local/share/ox/worktrees/<id>`.

### Evidence

`docs/images/hero-light.png` and `hero-dark.png`; the implementer's hand-off reports the same.

### Action

Rejected: changing it needs a worktrees option threaded through the shared services fixture for a cosmetic path, which the user's instruction against needless complication weighs against. The parent session can raise it with the user if the path matters in the hero.

## F4. Scene turns keep their `end` string past `LoadScene`

Low · Construction · Rejected · `internal/fake/scene.go:57`

`sceneTurn` holds `End` as a string and `Approval` as a pointer valid only on `waiting`. `LoadScene` checks both, and `play` switches on the string again with no default.

### Evidence

`checkTurn` and `play` in `internal/fake/scene.go`.

### Action

Rejected: the plan's Decisions settle that the scene is checked once in `LoadScene` and playback trusts it. Converting turns into a closed type would add a type to a test-build-only path for no behavior.

## Checks run

- Read the implementer's Tests proved and Checks: the seven new `internal/fake` tests fail on the base (they do not compile there); `make test`, `make e2e` (185 passed, 5 skipped, `hero.spec.ts` among the skipped), `make screenshots`, and `make docs` passed. `make live` was skipped because nothing changes how Ox drives Claude Code, and a headless Chrome check through `make dev` because nothing changes how the page reaches `oxd`. Both skips are correct.
- Spot-checked removals in `internal/fake`, one at a time, each reverted:
  - removing `a.next++` fails `TestSceneWorkingTurn` and `TestSceneNoTurn`;
  - removing the contained-text check fails `TestLoadSceneRefuses`;
  - finishing a `waiting` turn without asking fails `TestSceneWaitingTurn`;
  - finishing a `working` turn fails `TestSceneWorkingTurn`;
  - dropping the usage update fails `TestSceneIdleTurn`;
  - not clearing `then` before running it passes, since every `then` either finishes (which clears it) or replaces itself; the clear keeps each step running once and costs nothing.
- `go test -count=1 ./internal/server`: passed, covering the echo refactor.
- `go vet -tags screenshots ./cmd/oxd`: passed.
- `docs/functional-requirements.md` is unchanged, as the plan's Requirement changes (none) require.
- Opened all four images: theme, three workspaces with every label and status, indented children, the parked and archived sessions, the transcript with the child's report and collapsed Edit row, the Go code block, the open `retry.go` under the Files tab, the usage bar, and the phone's approval card, which sits in the composer's tray as the page's layout intends.

## Verification 1

Checked the uncommitted tree after `/ox-work fix`, still on base `5dbd0b3`. Ran `go test -count=1 ./internal/fake ./internal/server` (passed), `go vet ./internal/fake` and `go vet -tags screenshots ./cmd/oxd` (passed), `dprint check` (passed), and `gofmt -l internal/fake cmd/oxd` (clean). `docs/functional-requirements.md` is still unchanged.

| ID | Status   | Evidence                                                                                                                                                                                  |
| -- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `Agent` holds one `script *script` (`internal/fake/fake.go:32`); `script` in `scene.go` holds `scene`, `session`, and `next`; `Scripted` sets it, `begin` and `play` read it. `TestScene*` pass. |
| F2 | Resolved | `docs/architecture.md:380` reads "Recognizes these test prompts:".                                                                                                                        |

No new findings. The fix touches only the field layout and one doc line, so no regression reaches the images or the e2e fixtures.

## Verdict

Ready to commit. F1 and F2 are resolved; F3 and F4 stay rejected.
