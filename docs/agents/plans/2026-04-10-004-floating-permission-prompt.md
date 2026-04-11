# Floating permission prompt widget

## Goal

Replace the broken inline permission prompt (which never rendered) with a self-contained floating `PermissionPromptView` that overlaps the conversation area above the input bar. This eliminates the `ComposerMode` abstraction entirely — the InputAreaView stays in chat mode permanently because the approval widget is a separate view with its own focus and input handling.

## Desired outcome

When a tool needs approval, a bordered prompt bar appears just above the InputAreaView, overlapping the bottom of the ConversationView. The user types their response (y/n/session/workspace/always) into the prompt's own TextField and presses Enter. The bar disappears and focus returns to the chat input. The conversation view is not resized or reflowed.

```
┌──────────────────────────────────────────────────┐
│                                                  │
│              ConversationView                    │
│                                                  │
│  ╭───────────────────────────────────────────╮   │  PermissionPromptView
│  │ Allow 'bash' to run 'ls'? (y/n):         │   │  (overlapped, Visible=false by default)
│  │ > █                                       │   │
│  ╰───────────────────────────────────────────╯   │
╭──────────────────────────────────────────────────╮
│ > █                                              │  InputAreaView (unchanged)
├──────────────────────────────────────────────────┤
│ ● ● ● ●   claude-sonnet-4-20250514                │
╰──────────────────────────────────────────────────╯
```

## Approaches considered

### Option A — Self-contained overlapped View (chosen)

- Summary: New `PermissionPromptView` with `ViewArrangement.Overlapped`, positioned above InputAreaView. Owns its own Label + TextField + TCS. PermissionHandler interacts with it directly.
- Pros: Matches the visual spec exactly. Eliminates ComposerMode. Self-contained — no shared state with InputAreaView. Uses documented Terminal.Gui `ViewArrangement.Overlapped` pattern (same as `Window` uses).
- Cons: Custom drawing code (but identical pattern to InputAreaView).
- Failure modes: Focus not restoring correctly after hide. Mitigated by explicit focus transfer in show/hide methods.

### Option B — Modal Dialog

- Summary: Use Terminal.Gui's `Dialog` class, run via `Application.Run(dialog)` inside `App.Invoke`.
- Pros: Most idiomatic for "ask question, block until answered." Focus/keyboard handling is free.
- Cons: Centers on screen — doesn't match the "floating above input" visual. Heavy chrome (border, shadow, title bar). Nested event loop inside `App.Invoke` is a different execution model.
- Failure modes: Fighting Dialog's default centering/sizing would be fragile and non-idiomatic.

## Recommended approach

- Why this approach: Option A matches the described visual, removes architectural complexity (ComposerMode), and uses the same custom-drawing pattern already established by InputAreaView. The `ViewArrangement.Overlapped` property is exactly what Terminal.Gui's `Window` class uses — it's a single documented flag, not a deviation.
- Key tradeoffs: Slightly more code than a Dialog, but all of it follows the existing InputAreaView pattern 1:1.

## Related code

- `src/Ox/Views/InputAreaView.cs` — Drawing pattern to replicate. Also: remove `SetPrompt` method.
- `src/Ox/Views/OxApp.cs` — Add PermissionPromptView to the view hierarchy. Expose via public property.
- `src/Ox/PermissionHandler.cs` — Rewrite to interact with PermissionPromptView instead of ComposerController's permission mode.
- `src/Ox/ComposerController.cs` — Remove `ComposerMode` enum, `EnterPermissionMode`, `ExitPermissionMode`, `_pendingPermission` field, and mode checks in `OnViewSubmit`/`OnViewEof`.
- `src/Ox/Views/InputAreaView.cs` — Remove mode-aware Tab gating in `OnTextFieldKeyDown` (no longer needed since permission input goes to a different view).

## Current state

- `InputAreaView.SetPrompt(string)` exists but never stores or renders the prompt text — it only calls `SetNeedsDraw()` with nothing to draw. This is why approval prompts are invisible.
- `ComposerController` has a dual-mode system (`Chat`/`Permission`) that switches the single TextField between chat input and permission input. The mode-switching logic works correctly but the visual side was never implemented.
- `PermissionHandler` uses a TCS-via-Invoke pattern to bridge the background thread and UI thread. This pattern is sound and will be preserved — only the target changes from ComposerController to PermissionPromptView.
- InputAreaView's custom drawing (rounded border, manual `Move`/`AddRune`/`SetAttribute` calls) is the established pattern for bordered widgets in this app.

## Structural considerations

**Hierarchy**: PermissionPromptView is a peer of ConversationView and InputAreaView inside OxApp. It overlaps ConversationView via Z-order (added after ConversationView in the SubView list). This is the same layering pattern Terminal.Gui's Window class uses.

**Abstraction**: The permission prompt is now a self-contained widget with a clear API surface: `ShowAsync(prompt, ct)` returns `Task<string?>`, `Hide()` dismisses it. No mode concept leaks into other components.

**Modularization**: Removing ComposerMode eliminates a cross-cutting concern that coupled InputAreaView, ComposerController, and PermissionHandler. Each component now has a single responsibility: InputAreaView handles chat input, PermissionPromptView handles permission input, ComposerController manages the chat channel.

**Encapsulation**: PermissionPromptView owns its own TCS and TextField internally. Neither PermissionHandler nor OxApp need to know about those internals — they only call `ShowAsync`/`Hide`.

## Refactoring

These refactors happen before or alongside the feature work. They remove the broken permission mode infrastructure so the new widget can slot in cleanly.

1. **Remove `ComposerMode` enum and all mode-switching logic from `ComposerController`**. `OnViewSubmit` becomes unconditionally chat-only. `OnViewEof` becomes unconditionally chat-only. `EnterPermissionMode`, `ExitPermissionMode`, `_pendingPermission`, and the `Mode` property are deleted.

2. **Remove `SetPrompt` from `InputAreaView`**. Remove the mode check in `OnTextFieldKeyDown` that gates Tab/autocomplete on `ComposerMode.Chat` — since the view is always in chat mode now, the guard is dead code.

## Implementation plan

### Phase 1: Create PermissionPromptView

- [ ] Create `src/Ox/Views/PermissionPromptView.cs`. The view:
  - Extends `View`
  - Sets `ViewArrangement = ViewArrangement.Overlapped` and `Visible = false` in constructor
  - Contains a `Label` (row 1, prompt text) and a `TextField` (row 2, user input with `> ` prefix)
  - Height = 4 rows: top border, prompt label, input row, bottom border
  - Width = `Dim.Fill()`
  - Custom drawing: identical rounded-border pattern to InputAreaView (same chars, same colors)
  - `CanFocus = true` so it can receive focus when visible
  - Wires `TextField.Accepting` to resolve the internal TCS
  - Wires `KeyDown` for Escape and Ctrl+C to resolve TCS with `null` (deny)

- [ ] Add public API:
  - `Task<string?> ShowAsync(string prompt, CancellationToken ct)` — sets label text, creates TCS, registers CT cancellation, sets `Visible = true`, transfers focus to the TextField, returns the TCS task
  - `void Hide()` — sets `Visible = false`, clears state. Focus restoration is handled by the caller (PermissionHandler) via `App.Invoke`.

### Phase 2: Wire into OxApp

- [ ] In `OxApp` constructor, create the PermissionPromptView and position it:
  - `Y = Pos.AnchorEnd(InputAreaHeight + PermissionPromptHeight)` (directly above InputAreaView)
  - `Width = Dim.Fill()`
  - `Height = Dim.Absolute(PermissionPromptHeight)` where `PermissionPromptHeight = 4`
  - Add it to OxApp **after** ConversationView so it renders on top (Z-order is SubView list order)
  - Expose via `public PermissionPromptView PermissionPromptView { get; }`

### Phase 3: Update PermissionHandler

- [ ] Rewrite `PermissionHandler.Build` to use `PermissionPromptView` instead of `ComposerController`:
  - Step 1 (App.Invoke): Call `oxApp.PermissionPromptView.ShowAsync(promptText, ct)`, propagate the task via TCS back to the background thread (same Invoke/TCS bridge pattern as today)
  - Step 2 (background): Await the permission task
  - Step 3 (App.Invoke): Call `oxApp.PermissionPromptView.Hide()`, transfer focus back to `oxApp.InputAreaView`
  - Parse the response string identically to today

### Phase 4: Remove dead permission mode code

- [ ] In `ComposerController.cs`: delete the `ComposerMode` enum, `Mode` property, `_pendingPermission` field, `EnterPermissionMode`, and `ExitPermissionMode`. Simplify `OnViewSubmit` to always write to the chat channel. Simplify `OnViewEof` to always complete the chat channel.
- [ ] In `InputAreaView.cs`: delete `SetPrompt`. Remove the `_controller?.Mode == ComposerMode.Chat` guard from the Tab handler in `OnTextFieldKeyDown` — Tab/autocomplete always applies now.

### Phase 5: Validate

- [ ] `dotnet build` compiles cleanly with no warnings
- [ ] Existing tests pass (`dotnet test`)
- [ ] Manual test: start a conversation that triggers a tool call. Verify the permission prompt appears above the input area, accepts input, and disappears after responding. Verify focus returns to the chat input.
- [ ] Manual test: press Escape or Ctrl+C while the prompt is visible — verify it denies the permission and disappears.
- [ ] Manual test: verify normal chat input still works correctly after a permission prompt cycle.

## Open questions

- Should the prompt bar have a distinct background color or border color to visually distinguish it from the input area? (Current plan: same border style as InputAreaView for consistency, but a different text color for the prompt could help it stand out.)
