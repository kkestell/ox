# Event-Driven Composer Queue

## Goal

Replace the current `ReadLineAsync`-based composer bridge with a Terminal.Gui-native input flow:

- the composer stays alive all the time
- Enter is handled through `Command.Accept` / `Accepting`
- submitted chat messages are pushed into a queue consumed by the background turn loop
- permission requests use a distinct UI mode instead of piggybacking on the chat field's read lifecycle

## Desired outcome

- No `App.Invoke(async () => ...)` anywhere in the Ox input path.
- No arm/disarm waiter or "pending read vs buffered submission" state machine.
- Typing ahead between turns works because submissions are queued intentionally, not because a dropped-input bug is patched over.
- Permission prompts cannot steal queued chat input.
- Tests describe user-visible behavior at the queue/mode boundary instead of `TaskCompletionSource` choreography.
- `boo` can verify the interactive flow before implementation is considered done.

## How we got here

The current design treats `InputAreaView` like a pull-based `Console.ReadLine()` surface even though Terminal.Gui is a push-based command/event system. Local exploration confirmed the mismatch the user called out:

- [`src/Ox/Views/InputAreaView.cs`](src/Ox/Views/InputAreaView.cs) owns `ReadLineAsync`, `_submissionBuffer`, prompt prefixes, and raw `KeyDown` submission handling.
- [`src/Ox/Program.cs`](src/Ox/Program.cs) awaits input by wrapping `ReadLineAsync` inside `App.Invoke(async () => ...)`.
- [`src/Ox/PermissionHandler.cs`](src/Ox/PermissionHandler.cs) repeats the same pattern for permission prompts, layering a second read contract onto the same field.
- [`tests/Ur.Tests/InputSubmissionBufferTests.cs`](tests/Ur.Tests/InputSubmissionBufferTests.cs) and [`tests/Ur.Tests/InputSubmissionRaceTests.cs`](tests/Ur.Tests/InputSubmissionRaceTests.cs) document races created by that architecture rather than a clean framework-aligned contract.

The recommended plan keeps the user's direction, but narrows it to the smallest structural refactor that actually fixes the mismatch: queue-backed chat submission plus a dedicated permission mode, without introducing a generic modal framework yet.

## Approaches considered

### Option 1

- Summary: Keep `ReadLineAsync`, keep the shared field, and continue patching gaps around pending reads and UI-thread continuations.
- Pros:
  - Smallest immediate diff.
  - Minimal renaming or file movement.
- Cons:
  - Preserves the wrong abstraction boundary.
  - More key handling edge cases will continue to show up as races between "UI event happened" and "reader got armed."
  - `Program` and `PermissionHandler` stay coupled to async work running through UI-thread callbacks.
- Failure modes:
  - Enter works until a new timing edge appears.
  - Prompt-specific behavior keeps leaking into chat behavior and vice versa.

### Option 2

- Summary: Introduce a queue-backed composer controller. `InputAreaView` raises high-level submissions through Terminal.Gui's accept path, the background loop consumes chat submissions from a channel, and permission requests switch the composer into a dedicated permission mode with its own completion task.
- Pros:
  - Fixes the actual abstraction mismatch.
  - Removes `ReadLineAsync`, `_submissionBuffer`, and async work inside `App.Invoke`.
  - Keeps the change local to Ox without building a generic modal/dialog layer first.
- Cons:
  - Adds one new coordinating type between the view and the REPL loop.
  - `InputAreaView` still needs explicit mode-aware behavior for chat vs permission entry.
- Failure modes:
  - If mode transitions are sloppy, chat-only shortcuts could leak into permission mode.
  - If the queue/controller seam is vague, responsibility could drift back into the view.

### Option 3

- Summary: Do Option 2, but also introduce a true permission dialog/modal instead of reusing the composer surface.
- Pros:
  - Strongest separation between "chat composition" and "permission decision."
  - Most future-proof if more blocking workflows are expected.
- Cons:
  - Bigger UI refactor.
  - No existing Ox modal infrastructure to follow, so this pulls the plan into a second problem.
- Failure modes:
  - Focus restoration and modal lifecycle bugs overshadow the original input fix.
  - The repo grows a generic dialog abstraction before there is a second use case for it.

## Recommended approach

- Why this approach:
  - Option 2 fixes the real problem now: it matches Terminal.Gui's event model, makes the queue explicit, and removes the `async`-inside-`Invoke` shape that is currently carrying too much risk.
  - It is also the cleanest PHAME fit. The view becomes a widget again, the controller owns the interaction workflow, and the background loop consumes a queue instead of arm/disarm callbacks.
  - It leaves the door open to promote permission handling into a true modal later without changing the chat submission contract again.
- Key tradeoffs:
  - Permission handling stays in the existing composer region for now, but as a separate mode rather than another read layered onto the same field.
  - The plan deliberately avoids building a generic modal system until the repo has a second concrete use for one.

## Related code

- `src/Ox/Views/InputAreaView.cs` — Current rewrite target; mixes widget concerns with async read orchestration.
- `src/Ox/Views/InputSubmissionBuffer.cs` — Compensating state machine that should disappear once chat submission is explicitly queued.
- `src/Ox/Program.cs` — REPL loop currently waits for input through `TaskCompletionSource` plus `App.Invoke(async ...)`.
- `src/Ox/PermissionHandler.cs` — Permission path currently re-enters the same shared composer through `ReadLineAsync`.
- `src/Ox/Views/OxApp.cs` — Root layout and focus owner; likely home for wiring the view to the new controller.
- `tests/Ur.Tests/InputSubmissionBufferTests.cs` — Existing regression intent around typed-ahead submissions; replace with queue-level tests.
- `tests/Ur.Tests/InputSubmissionRaceTests.cs` — Existing regression intent around UI-thread/TCS races; retire or rewrite once those mechanics are removed.
- `tests/Ur.Tests/InputAreaViewTests.cs` — Extend with mode/accept-path tests if practical, while keeping palette/status tests.
- `.ignored/ox-boo-harness/Program.cs` — Existing harness entry point; likely needs expansion so `boo` can exercise chat and permission flows deterministically.
- `boo/README.md` — Documents the supported PTY automation surface that implementation must use for final verification.

## Current state

- `InputAreaView` owns both presentation and workflow state: prompt prefix, autocomplete callback plumbing, pending-read lifecycle, and submission buffering.
- Enter submission is handled via raw `KeyDown`, not the framework's accept command path.
- `Tab`, `Ctrl+C`, and `Ctrl+D` are conditionally active based on whether a synthetic read is pending.
- `Program.RunReplLoop` creates an outer `TaskCompletionSource<string?>` and resolves it from an `async` callback passed to `App.Invoke`.
- `PermissionHandler` duplicates that pattern with another `TaskCompletionSource<string?>`, so chat and permission reads share the same low-level field and cancellation mechanics.
- The current tests lock in several timing fixes that become unnecessary once submission is modeled as a queue instead of a pending reader.

## Structural considerations

**Hierarchy**

The UI thread should produce user intents; the background turn loop should consume them. `Program` should no longer await work that is itself running inside the UI callback queue.

**Abstraction**

`InputAreaView` is a widget, not a read API. A separate coordinator should own "what a submitted line means right now" and whether it feeds chat or completes a permission request.

**Modularization**

The clean seam is a dedicated controller for composer workflow. That keeps Terminal.Gui-specific event handling out of `Program` and keeps async queue/permission logic out of the view.

**Encapsulation**

The view should expose high-level submission hooks or accept a controller callback. It should not expose `TextField` internals or encode application-level state such as "chat reads may drain but permission reads may not."

## Refactoring

The refactor should establish the new seam before deleting the old one.

- Add a controller type, tentatively `ComposerController`, that owns:
  - a `Channel<ComposerSubmission>` (or similarly named queue contract) for chat input
  - the active composer mode (`Chat` vs `Permission`)
  - the lifecycle of one pending permission interaction at a time
- Introduce explicit mode/value types instead of bool-heavy flow:
  - `ComposerMode`
  - `ComposerSubmission`
  - `PermissionPromptSession` or equivalent
- Move autocomplete ownership fully into the view/controller boundary so `ReadLineAsync` no longer has to thread an external completion callback.
- Delete `InputSubmissionBuffer` only after queue-backed tests are in place and `Program`/`PermissionHandler` no longer call `ReadLineAsync`.

## Research

### Repo findings

- `InputAreaView` already keeps the text field alive between turns; the problem is the read contract layered on top of it, not the always-live composer itself.
- The repo already uses channels in other asynchronous orchestration code (`src/Ur/AgentLoop/ToolInvoker.cs`), so a queue-backed boundary is not foreign to the codebase.
- The existing boo harness is present but currently static; it will need a little more scripting surface to verify the interaction refactor.

### External research

- Skipped. The repo and the user's Terminal.Gui notes are sufficient for this plan, and there is no need to introduce new framework surface area before implementation proves local patterns are insufficient.

## Implementation plan

- [ ] Add or rewrite regression tests first so the new contract is explicit before the old machinery is removed.
  - Cover "chat submissions queue even when no turn is actively waiting."
  - Cover "permission mode does not consume queued chat submissions."
  - Cover "Escape only cancels an active turn when the composer is in chat mode."
  - Prefer testing the new controller seam over testing `TaskCompletionSource` timing details.
- [ ] Introduce a dedicated composer workflow coordinator.
  - Create `ComposerController` (name can change, responsibility should not).
  - Give it a chat submission channel and a single pending permission-session slot.
  - Document the ownership boundary in comments: the view emits intents, the controller interprets them, the REPL loop consumes the queue.
- [ ] Refactor `InputAreaView` into an always-live, event-driven widget.
  - Remove `ReadLineAsync`, `_submissionBuffer`, `_onCompletionChanged`, and prompt-driven read arming.
  - Handle Enter through the Terminal.Gui accept path (`Command.Accept`, `Accepting`, or `Accepted`) instead of raw `KeyDown` submission.
  - Keep `KeyDown` only for shortcuts the framework does not already model, and make those shortcuts mode-aware.
  - Disable autocomplete behavior in permission mode so `Tab` and suggestion state remain chat-only.
  - Update comments to explain why the view no longer owns async reads.
- [ ] Rewire `Program.RunReplLoop` around the queue.
  - Create the controller during app startup and bind it to `InputAreaView`.
  - Replace the outer input `TaskCompletionSource` with `await` on the controller's chat queue.
  - Keep `App.Invoke` only for synchronous UI mutations such as turn-state changes, entry creation, and focus/mode transitions.
  - Remove any remaining `async` lambdas passed into `App.Invoke`.
- [ ] Rewrite `PermissionHandler` around permission sessions instead of `ReadLineAsync`.
  - When a permission request arrives, synchronously switch the controller/view into permission mode on the UI thread.
  - Await the permission session task from background code without nesting async work inside `App.Invoke`.
  - Translate the submitted permission text into `PermissionResponse`, validate scopes exactly as today, then restore chat mode and focus.
  - Decide cancellation semantics explicitly and test them. The recommended default is: permission cancellation denies the request without killing the overall app loop.
- [ ] Delete obsolete buffering and race machinery.
  - Remove `src/Ox/Views/InputSubmissionBuffer.cs`.
  - Remove or rewrite tests that only exist to preserve the old waiter/TCS choreography.
  - Update stale comments in `InputAreaView`, `Program`, and `PermissionHandler` so the code explains the new architecture, not the retired workaround.
- [ ] Extend the boo harness so interactive verification is deterministic.
  - Update `.ignored/ox-boo-harness/Program.cs` or add a similar harness entry point that can exercise:
    - normal chat submission
    - typed-ahead submission while a fake turn is marked running
    - permission mode activation and response
  - Keep the harness self-contained so verification does not depend on a live provider or real tool execution.
- [ ] Run the full verification pass before declaring the refactor complete.
  - Unit tests first.
  - `make inspect` after the refactor stabilizes.
  - `boo` verification last, using the harness to confirm the end-to-end interaction shape.

## Impact assessment

- Code paths affected:
  - Ox input/composer flow
  - permission prompt flow
  - REPL turn-start / turn-end transitions
  - Escape / EOF handling around active turns
- Data or schema impact:
  - None.
- Dependency or API impact:
  - Internal Ox APIs will change substantially: `InputAreaView.ReadLineAsync` should disappear, `PermissionHandler.Build` will need the new controller dependency, and `Program` will consume a queue instead of a UI-thread-completed TCS.

## Validation

- Tests:
  - `dotnet test Ox.slnx`
  - Any newly added focused tests for the controller and permission-mode behavior should pass in isolation before running the full suite.
- Lint/format/typecheck:
  - `make inspect`
- Manual verification:
  - Build and run the Ox boo harness.
  - Use `uv run boo start "dotnet run --project .ignored/ox-boo-harness/OxBooHarness.csproj"` (or the equivalent harness command after refactoring).
  - Use `uv run boo type ...`, `uv run boo press enter`, and `uv run boo screen` to verify:
    - first submission works
    - repeated submissions continue working across multiple turns
    - typed-ahead submission is delivered after the running turn completes
    - permission mode accepts and denies correctly without consuming queued chat text
    - Enter still works after exiting permission mode

## Gaps and follow-up

- A true generic modal/dialog abstraction is intentionally out of scope here. If Ox later needs multiple blocking workflows, plan that separately on top of the queue/controller seam introduced here.
- This plan does not add multiline composition, input history, or richer permission affordances. The goal is to fix the interaction model first.
