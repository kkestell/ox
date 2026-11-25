# README hero screenshots

## Goal

The README shows what Ox looks like. `make screenshots` plays a scene file
through the real `oxd`, relay, and page, with a scripted harness in place of
Claude and Ur. It writes a desktop and a phone capture in light and dark to
`docs/images/`. Adding a session or workspace to the hero means editing the
scene, not the driver.

## Related code

- `internal/fake/fake.go`: the echo harness. Its turn is `thoughts`, then
  `pieces`, then `finish(reply)`, stepped by `tick` events, and it asks for
  approvals through `ask`. The scripted harness reuses all of it.
- `cmd/oxd/main.go`: builds the `harnesses` map and checks the configuration
  against it.
- `internal/server/session.go` (`report`, `deliverReports`) and
  `internal/server/mcp.go` (`createSession`, `childRecord`, `messageInput`):
  - A child's message arrives with `Content` wrapping the parent's text.
  - A child's report goes to its parent as one reports input, but only when
    the parent is free and none of its children is running.
  - A child takes its parent's permission mode, which the child's harness must
    offer.
- `internal/server/session.go` (`generateTitle`): asks the titling harness's
  `Ask` with the first prompt as text.
- `internal/server/server.go` (`New`): sessions loaded at startup are parked.
- `cmd/ox/web/e2e/services.ts`, `ui.ts`, `global-setup.ts`: the e2e services
  fixture, the `callTool` socket helper, and the race-enabled build.
- `cmd/ox/web/src/Panels.tsx`: the file tree starts hidden on a wide screen.
- `cmd/ox/web/src/SessionView.tsx`:
  - The header shows the session's directory.
  - `ToolCall` shows a call's name and summary, expanding to its input JSON.
  - `Usage` shows under the composer.
- `internal/config/default.json`: the built-in catalog the capture serves, so
  model names and harness icons are the real ones.

## Decisions

- **How oxd plays a scene: a build tag.**
  - `cmd/oxd/scene.go` (`//go:build screenshots`) declares a `-scene` flag. It
    defines `playScene(harnesses)`, which loads the scene with
    `fake.LoadScene` and sets `harnesses["claude"]` and `harnesses["ur"]` to
    `fake.Scripted(scene)`. It exits through `log.Fatal` when the scene cannot
    load, including when `-scene` is empty.
  - `cmd/oxd/noscene.go` (`//go:build !screenshots`) defines `playScene` as a
    no-op.
  - `main` calls `playScene(harnesses)` once, before `checkConfig`.
  - Normal builds have no `-scene` flag and no path to the scripted harness.
    The linker drops `internal/fake/scene.go` from them because nothing
    references it.
  - `make lint` adds `go vet -tags screenshots ./cmd/oxd`, so the tagged file
    cannot rot between captures.
- **How a scene drives updates: per-session scripts in the fake agent.**
  - Each scene session (top-level or child) has a `prompt` (top-level) or
    `message` (child), and an ordered list of `turns`.
  - A scripted agent binds to a scene session on its first input: the session
    whose prompt or message the input's `Content` contains. A child's message
    arrives wrapped, so the match is `strings.Contains`.
  - Each input the agent takes plays its session's next turn. A parent's turns
    after the first answer its children's reports, in order.
  - A first input that matches no session, or an input past the last turn,
    ends the turn with an error result saying the scene has no turn for it.
  - A turn holds these fields:
    - `updates`: literal `agent.Update` JSON objects of type
      `reasoning_delta`, `text_delta`, `tool_use`, or `tool_result`, published
      one per step with the turn's `MessageID` and the current time.
    - `reply`: Markdown streamed as text deltas through `split`. It is the
      result's text when the turn ends idle, which is what a child reports.
    - `usage`: windows as `interval_seconds`, `used_percent`, and
      `resets_in_minutes`, published first through `agent.UsageUpdate`, with
      `resets_at` computed when played.
    - `end`: `idle` publishes the result. `working` leaves the turn running
      with nothing scheduled. `waiting` asks to approve `approval`
      (`tool_name`, `input`) through the existing `ask`.
  - `Scripted(scene).Ask` returns the title of the top-level scene session
    whose prompt equals the text, or an error. Top-level labels are the
    scene's titles; children are labeled by `create_session`'s title.
  - `TakesProviders` is true, because the built-in Ur models name providers.
- **One turn shape for both fake modes.** In `fake.Agent`, `steps
  []agent.Update` and `then func()` replace `reply`, `thoughts`, and `pieces`.
  - `tick` publishes the next step. Once the steps are done, it takes `then`,
    clears it, and runs it. A nil `then` keeps the turn running.
  - An echo turn's steps are its reasoning deltas, then its reply deltas, and
    its `then` finishes with the reply.
  - A waiting scripted turn's `then` sets `then` to the finish and calls `ask`,
    so approving it finishes the turn as an echo approval does.
  - `finish`, `Interrupt`, and `Stop` clear both fields.
- **Validation at the boundary.** `LoadScene` refuses each of these, naming
  the session and turn:
  - an `end` other than `idle`, `working`, or `waiting`;
  - `approval` present except on `waiting`;
  - an update of any other type, since lifecycle updates belong to the
    agent's tracker;
  - a prompt or message that is empty, or that contains another session's.

  Playback trusts the loaded scene.
- **The scene file, `docs/screenshots/hero.json`.**
  - Top level: `workspaces` (each with `repo`, the fixture folder name, and
    `sessions`), plus `desktop` (`session` title and `file` path) and `phone`
    (`session` title).
  - A top-level session: `title`, `harness`, `model`, `effort`,
    `permission_mode`, optional `worktree`, `parked`, and `archived`, plus
    `prompt`, `turns`, and `children`.
  - A child: `name`, `title`, `harness`, `model`, `effort`, `message`, and
    `turns`.
  - Go reads only the fields it plays. The driver reads the rest.
- **The driver, `cmd/ox/web/e2e/hero.spec.ts`.**
  - Skipped unless `OX_SCREENSHOTS` is set. It uses `default.json` as its
    configuration and the scene through a new `scene` fixture option.
  - It copies each fixture repo to `<mkdtemp>/<repo>` and commits it with the
    e2e git environment. Then it adds each workspace in scene order through
    `workspace/create` over the socket.
  - It plays in two passes. Sessions marked `parked` play first. If any did,
    `services.restartDaemon(config)` reloads them parked. The rest play
    second.
  - Within each pass, a workspace's sessions are created in reverse scene
    order, so the sidebar lists them in scene order, with parked ones last.
  - Playing a session:
    1. `session/create` (with `worktree` when set), then `session/send` the
       prompt.
    2. Wait until it settles after one turn.
    3. For each child in order: `callTool(parent, "create_session", …)`, then
       wait until the child settles after all its turns.
    4. When the child's last turn ends idle, its report is the parent's next
       turn, so wait until the parent settles after one more.
    5. An `archived` session is archived through `session/archive` once it
       settles.
  - A session settles after n turns when `session/get` shows n `prompt`
    updates and the state of its nth turn's end: `idle`, `running`, or
    `requires_action`. The driver polls every 100 ms. If 15 s pass, it fails,
    naming the session and the condition. A scene whose reports cannot arrive
    fails this way.
  - Desktop context: 1600×1000 at device scale factor 2. The driver opens
    `#<desktop session id>` and turns on the Files toggle. For a workspace
    with archived sessions, it picks Show archived sessions from the
    workspace menu. It expands each folder of `desktop.file` and opens the
    file.
  - Phone context: the iPhone 15 descriptor without its browser type, opened
    at `#<phone session id>`.
  - Before each capture, the driver waits for:
    - every scene label in the sidebar (desktop);
    - the selected session's last reply or approval text;
    - the open file's text;
    - `document.fonts.ready`.
  - It captures with `page.emulateMedia({ colorScheme })` for `light`, then
    `dark`, and
    `screenshot({ path, animations: "disabled", caret: "hide" })`, to
    `docs/images/hero-{light,dark}.png` and
    `docs/images/hero-phone-{light,dark}.png`.
  - It removes the temporary repos when done.
- **Test infrastructure changes.**
  - `startServices(config, scene?)` passes `-scene=<path>` to every `oxd` it
    starts, restarts included.
  - `Services` gains `request(method, params)`, one JSON-RPC call over the
    socket. `callTool` shares its line reader.
  - `global-setup.ts` adds `-tags=screenshots` to the Go build when
    `OX_SCREENSHOTS` is set.
  - `ui.ts` exports the git environment `gitRepo` uses, as `gitEnv`.
- **`make screenshots`** runs
  `TMPDIR=/tmp OX_SCREENSHOTS=1 npm --prefix cmd/ox/web run e2e -- hero`.
  `/tmp` keeps the header's directory short. `make e2e` runs the spec
  skipped, as it does `approval.spec.ts` without `OX_LIVE`.
- **The desktop scene.**
  - `field-sync` holds the parent: Claude Opus, `auto` permission mode, since
    a Ur child must take a mode Ur offers, and `worktree: true`. Both children
    work in the parent's worktree, as every child does.
    - Turn 1, ending idle: the prompt, reasoning, a Read and an Edit of
      `internal/sync/retry.go`, a `CreateSession` call for the review, and a
      reply with a `go` code block. The Edit row stays collapsed.
    - Child 1: the Ur DeepSeek review, ending idle. Its report becomes turn 2.
    - Turn 2, ending working: reasoning, a `CreateSession` call for the
      tests, and a running `Bash` call (`go test ./...`, no result).
    - Child 2: a Sonnet child writing tests, ending working.
    - The parent's turns carry `usage`.
  - `field-sync` also holds a parked Claude Sonnet session.
  - `harbor-themes` holds a Claude session ending `waiting` on `Bash`
    (`npm run build`), with `usage`, and an idle Ur session.
  - `notes` holds one archived Claude Haiku session.
  - `desktop.file` is `internal/sync/retry.go`. `phone.session` is the
    `harbor-themes` waiting session.
- **Fixture repos** under `docs/screenshots/repos/`:
  - `field-sync`: `go.mod`, `README.md`, `cmd/field-sync/main.go`,
    `internal/sync/client.go`, `internal/sync/retry.go`, and
    `internal/sync/retry_test.go`, all gofmt-clean. The root `gofmt -l .`
    reads them. The nested `go.mod` keeps them out of the root module's
    `./...`.
  - `harbor-themes`: `package.json`, `README.md`, `src/tokens.css`, and
    `src/themes/harbor-dark.css`.
  - `notes`: two Markdown files. `make docs` formats them, as it does all of
    `docs/**/*.md`.
- **README.**
  - The desktop `<picture>` (dark `<source>`, light `<img>`) goes after the
    intro paragraph, before Features. Its alt text: "Ox with sessions across
    three workspaces: a working Claude session with two child sessions, one
    reporting back, a parked session, a session waiting on approval, and an
    archived session, beside the transcript and an open file".
  - The phone `<picture>` with `<img width="300">` goes after the Features
    list. Its alt text: "Ox on a phone, showing a Claude session asking to
    run a Bash command".
  - Development lists `make screenshots`.
- **Construction.**
  - The scene's shape is checked once in `LoadScene`.
  - A scripted agent is bound when its bound scene session is non-nil, and
    plays turn `next` of it.
  - A turn's ending is the one `then` closure, so no flag and no zero value
    stands for it.
- **Ownership.** The new agent fields are `steps`, `then`, the bound scene
  session, and `next`. Each belongs to the agent and is touched only in
  `Send`, `Handle`, `Approve`, `Interrupt`, and `Stop`, all on the session
  goroutine. Ticks reuse the existing `after` timers, which `done` cancels.
  Nothing is stored. The capture's temporary files are removed by the spec
  and the services fixture.
- **Trust boundaries.** `-scene` exists only in `-tags screenshots` builds and
  reads the file named on `oxd`'s command line.
- **Requirements.** None change: the capture runs a test build, and the
  echo-mode refactor keeps the fake's behavior.

## Check your assumptions

The user answered both questions:

1. **Worktree.** The Opus parent runs in its own worktree, and its children
   share it.
2. **Diff.** The hero shows no diff. The Edit call stays a collapsed row, and
   the reply's code block is Go.

## Test plan

- Scene loading and playback in a new `internal/fake/scene_test.go`. The fake
  package has no tests, and no server test reaches the tagged build. The test
  starts `Scripted(scene).Start` with a collecting `OnUpdate`, drives
  `Events` into `Handle`, and asserts through updates and `Snapshot`:
  - A prompt binds its session. An idle turn publishes its usage, its
    updates in order, its reply deltas, and a result with the reply's text,
    then state `idle`.
  - The next input plays the next turn. A `working` turn publishes no result
    and stays `running`.
  - A `waiting` turn leaves one approval for its tool in `Snapshot` and state
    `requires_action`. `Approve` then finishes it.
  - A child's wrapped message binds the child's session.
  - An unmatched first input, and an input past the last turn, each end with
    an error result.
  - `Ask` returns a top-level session's title for its prompt, and an error
    otherwise.
  - `LoadScene` refuses each invalid case listed under Decisions.
- The echo refactor: existing server tests (`internal/server`) and e2e
  specs that use `think`, `usage`, and `approve` prompts, unchanged, through
  `make test` and `make e2e`.
- The tagged build: `go vet -tags screenshots ./cmd/oxd` in `make lint`.
- The capture: `make screenshots` produces all four images. The implementer
  opens each one and checks:
  - the theme;
  - sidebar content: three workspaces, every label and status, the children
    indented, the archived session shown;
  - the selected transcript and the open file;
  - the usage bar;
  - on the phone, the approval card;
  - no clipped or overlapping layout.

## Implementation plan

1. `internal/fake/fake.go`: replace `reply`, `thoughts`, and `pieces` with
   `steps` and `then`, as in Decisions. Update the `tick` handling, `begin`,
   `Approve`, `finish`, `Interrupt`, and `Stop`. Add the `scene`, bound
   session, and `next` fields. `begin` calls `play(input)` when `scene` is
   set.
2. `internal/fake/scene.go`: `Scene`, `LoadScene`, `Scripted`, `play`, and
   the title lookup.
3. `internal/fake/scene_test.go`: the cases in the Test plan.
4. `cmd/oxd/main.go`: call `playScene(harnesses)` before `checkConfig`. Then
   add `cmd/oxd/scene.go` and `cmd/oxd/noscene.go`.
5. `Makefile`: add `go vet -tags screenshots ./cmd/oxd` to `lint`. Add the
   `screenshots` target with a comment, and add it to `.PHONY`.
6. `cmd/ox/web/e2e/global-setup.ts`, `services.ts`, `ui.ts`: the tag, the
   `scene` option, `request`, and `gitEnv`.
7. `docs/screenshots/repos/{field-sync,harbor-themes,notes}/` and
   `docs/screenshots/hero.json`.
8. `cmd/ox/web/e2e/hero.spec.ts`: the driver.
9. Run `make screenshots`. Inspect the four images in `docs/images/` and
   adjust the scene until each reads cleanly.
10. `README.md`: the two pictures and `make screenshots` under Development.
11. Documentation updates below. Then run `make docs`, `make test`, and
    `make e2e`.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, "The Fake agent":
  - Add the `think <text>`, `usage`, and `usage reset` prompts to the table,
    which lists three of the five today.
  - Add a paragraph: the scripted mode plays a scene's turns per session, and
    `oxd` built with `-tags screenshots` runs it as `claude` and `ur` under
    `-scene`.
  - Update the package table's `fake` row to match.
- `cmd/ox/web/AGENTS.md`: `hero.spec.ts` plays `docs/screenshots/hero.json`
  and runs with `make screenshots`.
- `AGENTS.md` source map: add `cmd/oxd/scene.go`, `cmd/oxd/noscene.go`,
  `internal/fake/scene.go`, `internal/fake/scene_test.go`,
  `docs/screenshots` (scene and fixture repos), and `docs/images` (README
  captures).
