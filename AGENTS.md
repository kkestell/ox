# Agent instructions

## Writing code

- Wait on events. A process announces readiness, state changes publish to a
  channel or watch, and code that needs to know a process exited waits on it
  or on a pipe it holds. Timers serve only as timeouts on a wait, or where the
  passage of time is itself the behavior, such as an idle period.
  - `oxd` prints `ready` once it listens; `connect_or_start` reads that line.
  - Idle exit watches a `watch::Sender<bool>` updated whenever clients or
    sessions change, then starts its idle timer.
- Production code serves production. Tests drive the same interfaces users
  do; flags, environment variables, methods, and modes exist because a user
  of the program needs them. Test fixtures live with the tests.
  - A session stuck in startup is closed with `session.close`, the method a
    UI uses, rather than through a test-only timeout flag.
  - Tests build text prompts with a helper in the test file; `Client` has
    one `prompt` method.
- The project has no external users. Change an interface by updating every
  caller; there is no old form to preserve.
- Versions stay at 1 until the project is in production. The client–daemon
  `hello` handshake checks the protocol version, but no change bumps it, and
  there are no backwards-compatibility paths.
  - `session.prompt` changed from `text` to content blocks, and every caller
    changed with it.
- Fix causes. A fix that needs a grace period, a retry loop, or a change to
  process-wide state points at a structural cause; find it and fix that.
  - After `claude` exits, everything it wrote is already in the stderr pipe,
    so the bridge reads what is buffered instead of waiting a grace period.
  - The socket's mode is set with `chmod` after `bind`, and the peer-uid
    check is what keeps other users out, so the process umask stays as is.
- Use what the language and libraries provide: derived serialization, `match`
  on enums, the library's ordering and lifecycle guarantees. Read the
  library's documentation for the guarantee before relying on it or working
  around its absence.
  - `ServerMessage` derives `Deserialize`; `oxctl` names a `StopReason` with
    a `match`.
  - The ACP library dispatches a notification before the response that
    follows it, so the response handler knows whether a notification already
    reported a config change.
- Take a plan's goals as the requirement and its mechanisms as suggestions.
  When a mechanism in the plan is weak, use a better one and say so.
  - The plan said `connect_or_start` "retries until it accepts"; the goal is
    connecting to a running daemon, which the `ready` line provides.
- A capability the code advertises is implemented in full.
  - An agent that advertises `loadSession` replays the conversation on
    `session/load`.
  - The bridge advertises the `image` prompt capability and sends images to
    Claude.
- The UI is idiomatic React (`ui/src/*.tsx`) with icons from `lucide-react`.
  The user designs the app's look: UI changes add only the CSS a layout needs
  to work, such as a scrolling region or `white-space: pre-wrap`, and no
  colours, spacing, borders, or other styling.

## Testing

- Each fix has a test that fails without it. Check by undoing the fix and
  running the test.
  - Making the stderr reader wait for end of file makes
    `failed_exit_reports_claude_stderr` time out.
  - Making idle ignore sessions makes `lifetime` fail.
- A test that waits for something confirms it happened: that the process
  started before checking it stopped, that the event arrived before checking
  the state it sets.
  - `close_while_starting` waits for the agent to open its FIFO before
    closing the session, then waits for the FIFO to reach end of file.
  - `cancellation_stops_a_pending_permission` waits for the prompt's
    `cancelled` response, which follows Claude withdrawing the request,
    before it answers the withdrawn request.

## Reporting

- Name every shortcut, workaround, and unimplemented part in the report, in
  plain terms.
  - "`session/load` returns no history although the bridge advertises it."
  - "`prompt_text` exists only so tests can send one string."

## Verifying changes

Run checks once, when a task is finished, and report their results. While
the user is reviewing a change and asking for adjustments, make the
adjustments without running checks, and run them when the user says the work
is done or asks for them.

Run the targets for the parts that changed:

- `make check-rust` checks Rust formatting and runs clippy with warnings as
  errors and the workspace tests.
- `make check-ui` runs Biome and `tsc` on the UI.
- `make check-scripts` runs ruff on `scripts/`.
- `make check` runs all three.

`make fix` applies the formatters and safe lint fixes.

After changing the behaviour of `crates/ox` or of the UI's TypeScript, also
run `make e2e` and report its result. It builds the app with its embedded
WebDriver server and drives it through the tests in `ui/e2e`, against sessions
of `oxd-fake-agent` that the tests start with `oxctl`. A change to
`ui/src/styles.css` alone takes `make check-ui`.

After changing `oxd`, `ox-protocol`, `oxctl`, `claude-code-acp`, or
`scripts/smoke-acp.py`, also run these and report their results:

```sh
make smoke-claude     # Haiku
make smoke-opencode   # openai/gpt-6-luna
```

The smoke checks run real models through `oxd` and `oxctl` and are part of
verifying every such change. Run them yourself; a failing smoke check is a
failure of the change.
