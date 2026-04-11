# Implement /model command with autocomplete

## Goal

Implement the `/model` slash command so users can switch the active model mid-session. When the user types `/model ` followed by any characters, autocomplete should suggest matching `provider/model` IDs from `providers.json`. Pressing Tab completes the suggestion, and pressing Enter with a valid model ID switches to that model immediately.

## Desired outcome

- `/model` with no argument cannot be submitted (Enter is a no-op until a valid argument is present).
- `/model openai/gpt-5.4` switches the active model and confirms in the conversation view.
- `/model open` + Tab completes to `/model openai/gpt-5.4` (first alphabetical match).
- Ghost text appears after `/model ` as the user types, just like command-name autocomplete.
- `/model nonexistent/bad` cannot be submitted — an invalid model ID is blocked at input time, not at execution time.

## How we got here

The original plan put model validation and persistence in `OxApp.SubmitInput()` with a dedicated `ModelCompleter` class. Architectural review identified three problems:

1. **Command execution leaked into the TUI layer.** `/model` is a configuration concern, not a TUI concern. Validation ("is this model ID known?") and persistence (`SetSelectedModelAsync`) are business logic that belongs in Ur, not Ox. OxApp should dispatch and display — not decide what constitutes a valid model.

2. **Dual command interception got worse.** OxApp already intercepts built-in commands at line 314 (stubs), and `UrSession.TryExpandSlashCommand` intercepts them again at line 314 (log and swallow). The original plan doubled down on OxApp as the execution point, making the session-layer interception even more vestigial.

3. **`ModelCompleter` hardcoded command knowledge.** A UI completion class that checks for the literal string `/model ` couples it to a specific command name. When `/set` needs argument completion, you'd need another parallel class.

## Approaches considered

### Option A — ModelCompleter in Ox, execution in OxApp

- Summary: Dedicated `ModelCompleter` class. Model validation and `SetSelectedModelAsync` called directly from OxApp.
- Pros: Simple. Few files changed.
- Cons: Business logic in TUI layer. Hardcoded command name in completer. Deepens the dual-dispatch problem.
- Failure modes: Every new built-in command that needs arguments requires a new completer class and more business logic in OxApp.

### Option B — General argument completions in AutocompleteEngine, execution in UrSession

- Summary: Extend `AutocompleteEngine` to accept per-command argument completion lists. Add `ExecuteBuiltInCommand` to `UrSession` for command execution. OxApp dispatches and displays.
- Pros: Autocomplete is general-purpose (no command-specific classes). Business logic stays in Ur. Clean layer boundary. `/set` gets argument completion for free later by adding a dictionary entry.
- Cons: Slightly more work upfront. `AutocompleteEngine` gains a second responsibility (argument completion alongside command-name completion).
- Failure modes: If argument completion ever needs something richer than prefix matching against a flat list (e.g., nested completions), the dictionary approach would need to evolve.

## Recommended approach

**Option B.** The extra upfront work is small (one dictionary parameter, one method on UrSession, one result type) and it respects the existing layer boundaries. The autocomplete engine stays command-agnostic — it just knows that some commands have completable argument lists. Command execution stays in Ur where it has access to configuration and validation logic. OxApp stays thin: dispatch and display.

## Related code

- `src/Ox/AutocompleteEngine.cs` — Extend with per-command argument completions.
- `src/Ox/Input/Autocomplete.cs` — No changes needed (delegates to engine, which now handles both phases).
- `src/Ox/OxApp.cs:65-80` — Constructor. Pass argument completions when building the engine.
- `src/Ox/OxApp.cs:294-336` — `SubmitInput()`. Replace stubs with delegation to `UrSession`.
- `src/Ox/OxApp.cs:600` — Render path. Ghost text already flows through. No changes.
- `src/Ox/Views/InputAreaView.cs:77-83` — Ghost text rendering. No changes.
- `src/Ox/Conversation/ConversationEntry.cs` — Add `StatusEntry` for informational messages.
- `src/Ox/Views/ConversationView.cs:330` — Add rendering case for `StatusEntry`.
- `src/Ur/Sessions/UrSession.cs:310-333` — `TryExpandSlashCommand()`. Update comment; guard remains.
- `src/Ur/Configuration/UrConfiguration.cs:85-86` — `ListAllModelIds()` for validation and completion.
- `src/Ur/Configuration/UrConfiguration.cs:109-113` — `SetSelectedModelAsync()` for persistence.
- `src/Ur/Hosting/UrHost.cs:41` — `Configuration` property. OxApp already has access.
- `src/Ur/Skills/BuiltInCommandRegistry.cs` — No changes.

## Current state

- `/model` is registered in `BuiltInCommandRegistry` and autocompletes as a command name.
- `SubmitInput()` in `OxApp.cs:314` catches `/model`, `/clear`, `/set` as stubs — shows "not yet implemented."
- `UrSession.TryExpandSlashCommand()` also intercepts built-ins but just logs and swallows (line 314). The comment says "actual execution is future work."
- `UrSession.ActiveModelId` snapshots the selected model at turn start, so a mid-session `/model` switch takes effect on the next turn. This is correct — no change needed.
- `AutocompleteEngine.GetCompletion()` returns `null` when the input contains a space, so `/model open` gets no ghost text today.
- `UrConfiguration.ListAllModelIds()` returns all `provider/model` IDs sorted alphabetically. Synchronous — loaded from `providers.json` at startup.
- Ghost text rendering and Tab-apply are already wired through `Autocomplete` -> `InputAreaView`. No rendering changes needed.

## Structural considerations

**Hierarchy.** Command execution moves from Ox (TUI) to Ur (core). OxApp calls down into `UrSession.ExecuteBuiltInCommand()` and displays the result — same direction as `StartTurn` calling `RunTurnAsync`. No upward dependency introduced.

**Abstraction.** `AutocompleteEngine` gains a second phase (argument completion) at the same level of abstraction as command-name completion: pure string-in, string-out prefix matching against a flat list. The two phases are distinguished by the presence of a space in the input — a clean structural boundary, not a special case.

**Modularization.** `ExecuteBuiltInCommand` on `UrSession` is the natural home — the session already holds `_configuration` and `_builtInCommands`. If the method grows with more commands, individual handlers can be extracted into private methods, but a separate class is premature now.

**Encapsulation.** `CommandResult` is a simple record produced by Ur and consumed by Ox. It carries a message and an error flag — no internal Ur state is exposed. The model list reaches the autocomplete engine as `IReadOnlyList<string>`, not as a `ProviderConfig` reference.

## Implementation plan

### Refactoring (before feature work)

- [x] **Add `CommandResult` record to Ur.** File: `src/Ur/Skills/CommandResult.cs`. Simple record: `public sealed record CommandResult(string Message, bool IsError = false)`. Lives alongside `BuiltInCommandRegistry` since it's the return type of built-in command execution.

- [x] **Add `ExecuteBuiltInCommand` to `UrSession`.** Public method: `CommandResult? ExecuteBuiltInCommand(string commandName, string? args)`. Returns null if the command name is not a recognized built-in (caller handles unknown commands). For now, `/clear` and `/set` return `new CommandResult("/{name} is not yet implemented.", IsError: true)` — same behavior as today, but from the right layer. `/model` gets full implementation (see below).

- [x] **Restructure `OxApp.SubmitInput()` command dispatch.** Replace the hardcoded stub block (lines 313-318) and unknown-command block (lines 320-322) with a three-way dispatch:
  1. `/quit` stays in OxApp (TUI exit concern).
  2. For built-in commands: call `_session!.ExecuteBuiltInCommand(command, args)`. If it returns a result, display it and return.
  3. If `ExecuteBuiltInCommand` returns null (not a built-in): check whether the command is a user-invocable skill via `_commandRegistry.UserInvocableNames`. If it is a skill, fall through to the normal turn path (do **not** return early — let `StartTurn` call `RunTurnAsync`, which calls `TryExpandSlashCommand` to expand it). If it is neither a built-in nor a skill, show "Unknown command" error.

- [x] **Update the guard comment in `UrSession.TryExpandSlashCommand()` (line 314).** Change "actual execution is future work" to reflect that execution now happens via `ExecuteBuiltInCommand`. The guard in `TryExpandSlashCommand` remains as a safety net for the `RunTurnAsync` path.

### Autocomplete extension

- [x] **Extend `AutocompleteEngine` with per-command argument completions.** Add a second constructor parameter: `IReadOnlyDictionary<string, IReadOnlyList<string>>? argumentCompletions = null`. Extend `GetCompletion()`:
  - Existing logic (no space): unchanged — prefix-match command names.
  - New logic (has space): parse the command name from input, look up in `argumentCompletions`. If found, prefix-match the argument portion (everything after the first space) against the completion list. Return suffix of first match, or null. Require at least one character after the space before suggesting.
  - The engine remains command-agnostic — it doesn't know what "model" means, just that the command named "model" has a list of completable arguments.

- [x] **Wire up argument completions in `OxApp` constructor (line ~80).** Build a dictionary: `new Dictionary<string, IReadOnlyList<string>> { ["model"] = host.Configuration.ListAllModelIds() }`. Pass it to `AutocompleteEngine` alongside the existing `CommandRegistry`. Retain the `CommandRegistry` as a field (`_commandRegistry`) so `SubmitInput` can check skill membership during dispatch.

### Feature work

- [x] **Add input-level validation for `/model` in `OxApp`.** Before submitting, if the command is `/model`, check the argument against `host.Configuration.ListAllModelIds()`. If the argument is missing or not in the list, suppress submission (return early from the Enter handler without calling `SubmitInput`). This keeps `ExecuteBuiltInCommand` free of error cases that should never reach it.

- [x] **Implement `/model` handler in `UrSession.ExecuteBuiltInCommand`.** Private method `ExecuteModelCommand(string args)`:
  - Argument is guaranteed non-null and valid by the time this is called (validated at input layer).
  - Call `_configuration.SetSelectedModelAsync(args).GetAwaiter().GetResult()` (synchronous — `SubmitInput` is called from the synchronous `HandleKey` path, and the underlying operation is a local file write). Return `CommandResult($"Model set to {args}.")`.

- [x] **Add `StatusEntry` to `ConversationEntry.cs`.** Informational (non-error) message entry for command confirmations. Same shape as `ErrorEntry` (single `Message` property), different semantic.

- [x] **Add `StatusEntry` rendering in `ConversationView.cs`.** Render with a neutral color (e.g., `_theme.StatusText` or `_theme.Text`) and `[info]` prefix, distinct from `ErrorEntry`'s red `[error]` prefix.

- [x] **Handle context window cache after model switch.** In `OxApp.SubmitInput()`, after a successful `/model` result, clear `_contextWindowCache` so the status line resolves the new model's context window on the next render cycle. The cache is cheap to rebuild (one synchronous lookup per model).

## Validation

- Tests:
  - [x] Unit test `AutocompleteEngine` argument completion: prefix matching works after `/model `, exact match returns null, no match returns null, no suggestion when argument is empty (just `/model `), case-insensitive command name matching, works for other registered commands too.
  - [x] Unit test `AutocompleteEngine` command-name completion is not regressed (existing behavior unchanged).
  - [x] Unit test `UrSession.ExecuteBuiltInCommand`: valid model sets selection and returns success, unrecognized command returns null. (Invalid and no-arg cases never reach `ExecuteBuiltInCommand` — validated at input layer.)
- Build: `dotnet build` must pass with no warnings.
- Manual verification:
  - Type `/mo` -> ghost text shows `del`. Tab completes to `/model`.
  - Type `/model ` -> no ghost text yet (no prefix typed).
  - Type `/model open` -> ghost text shows remainder of first match (e.g., `ai/gpt-5.4`).
  - Tab completes the full model ID.
  - Enter submits and shows `[info] Model set to openai/gpt-5.4`.
  - Status line updates to show new model name.
  - Type `/model` + Enter (no arg) -> Enter is a no-op; input is not submitted.
  - Type `/model nonexistent/bad` + Enter -> Enter is a no-op; input is not submitted.
  - Type the name of a user-invocable skill (e.g., `/commit`) -> ghost text completes it. Enter dispatches it as a normal turn; the skill prompt is expanded by `TryExpandSlashCommand`.
  - Type an unknown slash command -> "Unknown command" error is shown; no turn is started.

## Gaps and follow-up

- ~~**Skill dispatch from TUI.**~~ Resolved: the three-way dispatch in `SubmitInput()` (built-in → execute, skill → turn, unknown → error) handles this inline. No separate work needed.

## Open questions

- ~~Should `/model` with no argument list all available models, or just show the current one?~~ Resolved: `/model` without a valid argument cannot be submitted. Enter is a no-op until a recognized `provider/model` ID is present in the input.
