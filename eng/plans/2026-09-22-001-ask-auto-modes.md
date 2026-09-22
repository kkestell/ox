# Ask and Auto modes

Status: proposed.

## 1. Goal

Add a session mode that controls whether ACP prompts ask before running shell
commands:

- **Ask** preserves the current ACP behavior. Every shell tool call sends a
  permission request, and that call runs only when the user approves it.
- **Auto** runs every shell tool call without sending a permission request.

Ask is the default for a new ACP session. Read, search, and patch tools remain
automatic in both modes. Headless `ox run` remains automatic.

The selected mode is a durable session setting. A prompt captures it at the
turn boundary, so a change made while a turn is running applies to the next
turn. Loading a session restores the last saved mode, including Auto, and the
client shows that restored selection.

This version adds only Ask and Auto. It does not add Plan mode, command
classification, remembered per-command decisions, workspace defaults, or
per-user defaults.

## 2. ACP configuration surface

Add a third select option to the list returned by `session/new`,
`session/load`, and `session/set_config_option`:

| Option ID | Name | Category | Choice ID | Choice name | Description |
| --------- | ---- | -------- | --------- | ----------- | ----------- |
| `mode` | Mode | `mode` | `ask` | Ask | Ask before running each shell command. |
| `mode` | Mode | `mode` | `auto` | Auto | Run shell commands without asking. |

Use `SessionConfigOptionCategory::Mode`. Keep the option order Model, Effort,
Mode so the existing option positions remain stable.

`session/set_config_option` accepts only the two mode IDs. Any other value
returns `invalid_params` through the same validation path as model and effort
values. Returning the complete option list after a change continues to be the
source of the client's current selector state.

No separate confirmation is added when the user selects Auto. The selector is
the explicit authorization, and the ACP client remains responsible for how
prominently it presents a mode categorized as `mode`.

## 3. Session setting and transcript representation

Add a closed, serializable `SessionMode` enum in `src/sessions.rs`:

```rust
pub enum SessionMode {
    Ask,
    Auto,
}
```

Give it the same small ID, display-name, and parsing helpers as `EffortLevel`.
Extend `SessionSettings` and `SessionSettingsChange` with `mode` fields. The
normal default settings become the default model, Default effort, and Ask
mode.

Add `TranscriptEntry::Mode(SessionMode)` and store it with kind `mode`. The
transcript fold applies mode entries just as it applies effort entries. A
transcript without a mode entry uses the supplied default, which also keeps an
existing disposable database readable while local install targets continue to
recreate it under the project's compatibility policy.

At prompt startup, compare the captured mode with the last saved mode. When it
changed, append a mode entry in the same transaction as the user message. This
records the authorization policy in force for the turn and lets a later
`session/load` restore the actual selection.

Headless runs use Auto as their captured mode. Their first user-message
transaction therefore records Auto, making the saved transcript accurately
describe how their shell calls were authorized.

## 4. Transcript grammar and validation

Effort and mode can both change for one turn, so setting entries form one
contiguous block immediately before the user message they govern. Replace the
current effort-only adjacency rule with these rules:

- Every nonempty transcript still begins with exactly one model entry.
- After the opening model, a turn may have no setting entries, one effort or
  mode entry, or one of each before its user message.
- An effort or mode entry must be followed by another setting entry or by the
  user message for that turn.
- A settings block cannot contain the same setting kind twice.
- A settings block cannot be interrupted by an assistant message or tool
  result.
- Model entries remain forbidden after the transcript opens.

Use one fixed write order in `SessionStore::append_user`: model when needed,
then effort when changed, then mode when changed, then the user message.
Validation should accept the valid settings block as a block rather than rely
on that storage order. This keeps the invariant about meaning, not a private
serialization accident.

Replay continues to omit model, effort, and mode entries. Model requests also
continue to omit those entries; the mode controls local execution only.

## 5. Turn boundary and active-session state

Keep mode in the existing `ActiveSession.selections` value rather than adding
a parallel map or lock. The lifecycle then matches effort:

1. `session/new` seeds Ask.
2. `session/load` folds the transcript and seeds the last saved mode.
3. `session/set_config_option` updates the active selection immediately.
4. The prompt handler clones the complete selection before starting the
   prompt.
5. The prompt run saves any changed setting entries with the user message and
   uses the captured mode for every tool call in that turn.

A mode change during a running turn must not affect a permission request that
is already pending or later shell calls from that same turn. It affects the
next prompt because the active selection is copied only once at prompt
startup.

The existing operation guard remains unchanged. Configuration requests may
still complete while a prompt, load, or delete operation holds the session
guard.

## 6. Shell authorization

Make the captured `SessionMode` the single authority for shell approval inside
`PromptRun::approve`:

- Non-shell tools return approved in both modes.
- Auto returns approved for a shell call without sending
  `session/request_permission`.
- Ask sends the existing shell permission request and handles Approve, Deny,
  cancellation, unknown responses, and connection errors exactly as today.

The ACP connection is transport needed by Ask mode, not a second policy
selection. Refactor the current `ToolPermissions` input as needed so callers
cannot persist one mode while executing another. ACP prompt runs provide the
connection even when the captured mode is Auto. Headless prompt runs capture
Auto and do not need a permission-request connection. Treat an Ask prompt run
without an ACP connection as an internal invariant violation rather than
silently approving it.

Do not change the ordering around tool updates. A shell call is still announced
as pending first, becomes in progress immediately before execution, and gets
one final result. In Auto mode the only omitted exchange is the permission
request and response between pending and in progress.

Denial in Ask mode remains a failed tool outcome whose text says the user
denied permission. Auto mode has no synthetic approval outcome and adds no
transcript entry per shell call; the saved mode entry and ordinary tool result
are sufficient.

## 7. Cancellation and failure behavior

Preserve all current cleanup and commit boundaries:

- Cancellation before a tool starts prevents it from running in either mode.
- Cancellation while Ask is awaiting permission cancels the prompt as today.
- Cancellation during an automatically approved shell call reaches the same
  shell process-group cleanup path as an approved Ask call.
- An ACP update failure still stops new work and completes the uncommitted
  assistant batch with explicit outcomes.
- Auto mode does not turn tool failure into success or bypass transcript
  saving.

Switching to Auto removes only the permission round trip. It grants no new
operating-system permissions and adds no sandbox; the shell still runs with
Ox's existing process permissions and environment filtering.

## 8. Implementation scope

| File | Change |
| ---- | ------ |
| `src/sessions.rs` | Add `SessionMode`, add mode to settings and transcript entries, save and decode mode entries, fold saved mode, and validate pre-user settings blocks. |
| `src/acp.rs` | Add the Mode selector and setter, seed Ask for ACP sessions and Auto for headless runs, and pass the ACP connection as permission-request transport rather than policy. |
| `src/acp/prompt.rs` | Save mode changes at the turn boundary and make the captured mode select Ask or Auto behavior for shell calls. |
| `src/acp/convert.rs` | Keep the existing permission request unchanged; adjust only if names or call signatures move during the prompt refactor. |
| `eng/architecture.md` | Document mode as a durable setting, its turn snapshot, restored Auto behavior, and the mode-dependent ACP shell trust boundary. |
| `eng/glossary.md` | Define session mode, Ask mode, and Auto mode; update session settings and transcript-entry definitions. |
| `AGENTS.md` | Update the existing source-map descriptions for session settings, ACP selections, and prompt approval behavior. |

No schema table, dependency, migration, command-line flag, or new source module
is needed.

## 9. Test plan

Follow the repository's flat-test-budget rule. Extend or consolidate the
existing session-setting and shell-permission tests instead of adding one test
per branch.

### Session storage

Extend the existing transcript validation and settings-fold coverage to prove:

- Ask is the fallback when no mode entry exists.
- Auto survives storage, reading, and settings folding.
- effort and mode may both appear in one settings block before a user message;
- duplicate mode or effort entries in one block are rejected;
- a setting outside a pre-user settings block is rejected; and
- replay and OpenRouter message conversion omit mode entries.

Update existing store fixtures and `SessionSettingsChange` literals with mode
only where the type change requires it. Prefer `..Default::default()` for an
unchanged settings change when that makes the intended fields clearer.

### ACP configuration and turn boundary

Extend the current configuration tests to assert:

- a new session reports Ask as the current Mode value;
- `session/set_config_option` accepts Ask and Auto and rejects an unknown mode;
- `session/load` reports the last saved mode; and
- a mode selection changed during a running prompt applies only to the next
  prompt, with its transcript marker immediately before that prompt's user
  message.

Combine this with the existing effort turn-boundary test if doing so keeps one
clear test of the shared snapshot guarantee.

### Shell authorization

Extend `shell_permissions_control_execution_and_save_results` rather than
duplicating its transport harness:

- Keep all current Ask cases: approve, deny, mixed decisions, permission
  cancellation, prompt cancellation, EOF, unknown option, and request error.
- Add an Auto case that selects Auto before prompting, executes both shell
  calls, receives no `session/request_permission`, and saves completed results.
- Assert Auto still sends pending, in-progress, and final tool updates in call
  order.
- Confirm the saved transcript contains the Auto mode entry before the user
  message.

Keep the existing headless SIGINT and ACP shutdown tests as the owners of shell
cleanup guarantees. Adjust setup for the explicit mode but do not duplicate
their cancellation assertions.

After implementation, run `cargo test` and `cargo build`. Inspect the complete
test diff and report the test-function and test-code delta as required by
`eng/testing.md`.

## 10. Acceptance criteria

The change is complete when:

1. New ACP sessions show Ask by default.
2. Ask mode produces exactly the current shell permission flow.
3. Auto mode runs shell calls without any permission request while preserving
   all ordinary tool updates and results.
4. A mode change takes effect only at the next prompt boundary.
5. Mode changes are saved with the user message they govern and restored by
   `session/load`.
6. A loaded session visibly reports Auto before it can run an automatically
   approved shell command.
7. Headless runs remain automatic and record Auto in their transcripts.
8. Cancellation and shell cleanup behave identically after execution begins in
   Ask and Auto.
9. Architecture, glossary, and source-map documentation describe the shipped
   behavior.
10. `cargo test` and `cargo build` pass with no unexplained test-suite growth.
