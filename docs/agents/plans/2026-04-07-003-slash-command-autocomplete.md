# Slash Command Autocomplete

## Goal

Add inline ghost-text autocomplete for slash commands (both built-in commands and skills) to the chat input. When the user types `/` followed by one or more letters, the first matching command name is shown as gray ghost text starting at the cursor. Tab accepts the completion.

## Desired outcome

- Typing `/p` when only one command starts with "p" (e.g. `/plan`) shows `/p` in white, `lan` in gray, with the cursor block on the `l`.
- Typing `/p` when multiple commands start with "p" still shows the first match.
- Typing `/xyz` when nothing matches shows no ghost text.
- Pressing Tab with ghost text active inserts the completion and moves the cursor to end.
- Pressing Tab with no ghost text active is a no-op.
- A handful of built-in stub commands (`/quit`, `/model`, `/set`, `/clear`, plus a few deliberate stubs) exist for testing autocomplete behavior.

## How we got here

The feature spec arrived fully formed with clear acceptance criteria. Codebase exploration confirmed no autocomplete infrastructure exists; the approach below is the cleanest additive path through the existing architecture.

## Approaches considered

### Option A — Ghost text inline (recommended)

Render completion as gray cells immediately after the typed prefix, with the cursor block sitting on the first gray character.

- **Pros:** Matches the spec exactly; minimal new state (just a nullable suffix string); no dropdown/overlay complexity; works within the existing full-frame cell renderer.
- **Cons:** Cursor appears to "land on" a gray char rather than sitting after typed text — may feel slightly odd until completion is accepted.
- **Failure modes:** Viewport width clipping could cut off long completions; cursor cell style must visually distinguish gray char from white.

### Option B — Separate suggestion line below input

Show the full completion on the row below the input row (e.g. `  → /plan`).

- **Pros:** Cursor stays at end of typed text; no cell-style mixing.
- **Cons:** Requires reserving a row in the viewport layout; does not match the spec.

## Recommended approach

Option A — ghost text inline, per the spec. The implementation is additive: new `AutocompleteEngine`, a `BuiltInCommandRegistry`, a small delta to `InputReader`, and a small delta to `Viewport.RenderInputArea()`.

## Related code

- `src/Ur.Tui/InputReader.cs` — keystroke loop; Tab handling and completion callback go here
- `src/Ur.Tui/Viewport.cs` — `RenderInputArea()` renders the input row; ghost text cells added here
- `src/Ur/SkillRegistry.cs` — `UserInvocable()` returns skills eligible for "/" completion
- `src/Ur/SlashCommandParser.cs` — parses the name after "/"; autocomplete mirrors this logic
- `src/Ur/UrSession.cs` — dispatches slash commands; built-in commands will be intercepted here before skill lookup
- `src/Ur/CommandRegistry.cs` — new; unified ordered list of all user-invocable command names (built-ins then skills); the single thing `AutocompleteEngine` depends on

## Current state

- `InputReader.ReadLineInViewport` polls keystrokes, fires `onPromptChanged(prefix + buffer)` on each change. No Tab handling.
- `Viewport.RenderInputArea()` renders: chevron + white typed text + one reverse-video blank cell (cursor). Single-row, fixed layout.
- `SkillRegistry` has `.UserInvocable()` but no built-in command concept.
- `UrSession.TryExpandSlashCommand` handles skill lookup only; unknown commands fall through to the LLM.
- No `AutocompleteEngine`, no completion state, no ghost-text rendering.

## Structural considerations

**Hierarchy:** Which commands exist and in what priority order is a domain concern; it belongs in `Ur`. What to show the user given what they've typed is a TUI concern; it belongs in `Ur.Tui`. `CommandRegistry` in `Ur` owns the merge and ordering; `AutocompleteEngine` in `Ur.Tui` owns prefix matching against that list. The TUI layer depends on the domain layer — never the reverse.

**Modularization:** `AutocompleteEngine` takes a single `CommandRegistry` (one dependency, one responsibility: prefix match). It does not know that some commands are built-ins and others are skills. `CommandRegistry` takes `BuiltInCommandRegistry` and `SkillRegistry` and does not know anything about display.

**Encapsulation:** `InputReader` should not know how to render; it should communicate completion state via a second callback rather than writing to the viewport directly. `Viewport` should not know how completions are computed; it receives a nullable suffix string and renders it.

**Abstraction:** The `onPromptChanged` callback signature stays the same. A new `onCompletionChanged(string? suffix)` callback is added alongside it so the viewport can independently track what ghost text to show.

## Implementation plan

### Built-in command registry

- [x] Create `src/Ur/BuiltInCommand.cs` — record with `Name` (string) and `Description` (string).
- [x] Create `src/Ur/BuiltInCommandRegistry.cs` — holds a fixed ordered list of built-in commands; exposes `IReadOnlyList<BuiltInCommand> All`.
- [x] Register the following commands in `BuiltInCommandRegistry` (order determines priority when multiple names share a prefix):
  - `/clear` — clear conversation history
  - `/model` — switch model
  - `/quit` — exit the session
  - `/set` — configure a session setting
  - Stubs for multi-match testing (to be deleted after autocomplete is verified): `/clamp`, `/memo`, `/models`, `/quickfix`, `/query`
- [x] Register `BuiltInCommandRegistry` as a singleton in DI.
- [x] In `UrSession.TryExpandSlashCommand`, check `BuiltInCommandRegistry` first; if the name matches a built-in, log "built-in command (not yet implemented)" and return `true` to prevent the message from reaching the LLM. (Actual execution is follow-up work.)

### Command registry (unified view in `Ur`)

- [x] Create `src/Ur/CommandRegistry.cs` — takes `BuiltInCommandRegistry` and `SkillRegistry` in its constructor; exposes `IReadOnlyList<string> UserInvocableNames`, which is built-ins first (in registration order) then skill names (in load order). This is the only place where priority between built-ins and skills is defined.
- [x] Register `CommandRegistry` as a singleton in DI.

### Autocomplete engine

- [x] Create `src/Ur.Tui/AutocompleteEngine.cs`.
- [x] Constructor: `AutocompleteEngine(CommandRegistry commands)`. No knowledge of built-ins vs. skills.
- [x] Method: `string? GetCompletion(string buffer)`:
  - Return `null` if buffer does not start with `/` followed by at least one letter (i.e. pattern `^/[a-zA-Z]+`).
  - Extract the typed prefix (everything after `/`).
  - Search `commands.UserInvocableNames` for the first name that starts with the typed prefix (case-insensitive).
  - If found and the name is longer than the typed prefix, return the suffix (the remaining characters). Otherwise return `null`.
  - Multiple matches → return the first match's suffix (not `null`).
  - No matches → return `null`.

### InputReader changes

- [x] Inject `AutocompleteEngine` into `InputReader` constructor.
- [x] Add `Action<string?>? onCompletionChanged` parameter to `ReadLineInViewport` (alongside existing `onPromptChanged`).
- [x] After every keystroke that modifies the buffer, compute `AutocompleteEngine.GetCompletion(buffer)` and invoke `onCompletionChanged` with the result (or `null`).
- [x] Add Tab (`ConsoleKey.Tab`) handling in the keystroke switch:
  - Ask `AutocompleteEngine.GetCompletion(buffer)`.
  - If non-null, append the suffix to `buffer`, then invoke `onPromptChanged` and `onCompletionChanged(null)` (ghost text gone, buffer is complete).
  - If null, do nothing.
- [x] On `Enter` or `Backspace`, also invoke `onCompletionChanged(null)` to clear ghost text (backspace recalculates on next keystroke anyway, but Enter should clear immediately).

### Viewport changes

- [x] Add `_completionSuffix` field (`string?`) to `Viewport`.
- [x] Add `SetCompletion(string? suffix)` method: sets `_completionSuffix = suffix; _dirty = true`.
- [x] Wire `SetCompletion` as the `onCompletionChanged` callback when `InputReader.ReadLineInViewport` is called.
- [x] Update `RenderInputArea()`:
  - Render typed text in White as before.
  - If `_completionSuffix` is non-null and non-empty:
    - Render the first character of the suffix using `CellStyle.Reverse` (block cursor visual) over a `Color.BrightBlack` foreground — this gives the "cursor on gray char" appearance.
    - Render remaining suffix characters in `Color.BrightBlack` (plain gray, no reverse).
    - Do **not** render the extra blank reverse-video cursor cell (the cursor is already on the first ghost char).
  - If `_completionSuffix` is null or empty, render the blank reverse-video cursor cell as before.

### Boo tests

- [x] Add a boo test recipe to `docs/boo.md` — **Recipe 14: Slash command autocomplete**:
  1. `boo start ur` — boot session.
  2. `boo type /p` — type partial command.
  3. `boo screen` — verify ghost text shows the first matching completion after `/p` in gray.
  4. `boo press tab` — accept completion.
  5. `boo screen` — verify input shows `/plan` (or the matched command) with no ghost text and cursor at end.
  6. `boo press backspace` × N to clear; `boo type /c` — test `/c` prefix (matches `/clear`, `/clamp`).
  7. `boo screen` — verify first match (`/clear`) appears as ghost.
  8. `boo press backspace` × N; `boo type /xyz` — test no-match case.
  9. `boo screen` — verify no ghost text visible.
  10. `boo stop`.

## Validation

- **Unit tests** (xUnit):
  - `AutocompleteEngineTests` — test `GetCompletion` for: single match, multiple matches (returns first), no match, input without `/`, input `/` alone, exact match (full name typed).
  - `BuiltInCommandRegistryTests` — verify all expected commands present, correct order.
  - `CommandRegistryTests` — verify built-ins appear before skills, correct merged list.
- **Manual via boo**: Run Recipe 14 end-to-end. Visually confirm ghost text color, cursor position, Tab acceptance, and no-match silence.
- **Build**: `dotnet build` must pass with no warnings introduced.
