# Chat Input Top/Bottom Borders and Model Status Bar

## Goal

1. Change `ChatInput` to render only top and bottom borders (no side borders).
2. Add a new `ModelStatusBar` widget below the chat input that displays the active provider/model right-aligned.

## Desired outcome

```
─────────────────────────────────
❯ What is 1+1
─────────────────────────────────
                     openai/gpt-5
```

The chat input area has a clean, open look — no side pipes. A single-line status bar below it shows the current model ID right-aligned with no border of its own.

## Approaches considered

### Option A — Per-side border flags on `Widget` (Recommended)

Replace `bool Border` with four independent bools: `BorderTop`, `BorderBottom`, `BorderLeft`, `BorderRight`. `HorizontalChrome` sums left+right; `VerticalChrome` sums top+bottom. `Render` draws each edge independently. `ContentRect` insets only the sides that are active.

- **Pros:** Most composable — any combination works without an enum case. `ChatInput` sets only `BorderTop` and `BorderBottom`. Box-border widgets set all four. No combinatorial enum to maintain.
- **Cons:** Four properties instead of one; callers that want a full box set four flags (mitigated by a `SetBoxBorder()` convenience or just a helper constructor pattern).
- **Failure modes:** Off-by-one if `ContentRect` inset logic is wrong for a given combination; caught by existing `MeasureHeight` tests.

### Option B — `BorderStyle` enum

Add `enum BorderStyle { None, Box, TopBottom }`. Clean for the two known cases today, but requires a new enum member for every future combination (e.g., `LeftRight`, `Top`, `Bottom`).

- **Pros:** Single property.
- **Cons:** Enumerates combinations rather than composing sides — every new layout need adds a member.
- **Failure modes:** Same as Option A, plus the enum grows into a combinatorial explosion.

### Option C — Manual border drawing in `ChatInput.RenderContent`

Remove `Border = true`; draw `─` rules manually at the top and bottom of `RenderContent`. 

- **Pros:** Zero changes to `Widget`.
- **Cons:** `MeasureContentHeight` must account for the two extra rows it draws, coupling height measurement to rendering detail. Bypasses the chrome abstraction.

## Recommended approach

**Option A.** Per-side flags are the straightforward generalisation — composable, no enumerating combinations, and the implementation is a small mechanical extension of the existing chrome math. A new `ModelStatusBar` widget is a plain `Widget` subclass that renders a right-aligned string — one screenful of code.

## Related code

- `Ur.Terminal/Components/Widget.cs` — owns `Border`, chrome calculation, and `Render`; per-side flag changes land here
- `Ur.Terminal/Core/Buffer.cs` — `DrawBox` is used by `Widget`; a `DrawHRule` helper may be added here
- `Ur.Tui/Components/ChatInput.cs` — switches `Border = true` → `BorderTop = true; BorderBottom = true`; `MeasureContentHeight` still returns visual line count (chrome height added by base class)
- `Ur.Tui/ChatApp.cs` — `RenderFrame` adds `ModelStatusBar` to the `VerticalStack` after `ChatInput`
- `Ur.Tui/IChatBackend.cs` — may need `SelectedModelId` exposed so `ChatApp` can pass it to the status bar
- `Ur.Tui/UrChatBackend.cs` — adapter that wraps `UrConfiguration.SelectedModelId`

## Current state

- `Widget.Border` is a `bool`. `Render` calls `buffer.DrawBox` when true, and `ContentRect` subtracts 1 cell on all four sides. There is no per-side control today.
- `ChatInput` sets `Border = true` in its constructor. `MeasureContentHeight` returns `Math.Min(visualLines, MaxVisibleLines)` and the base-class `MeasureHeight` adds `VerticalChrome` (currently 2 for the box border).
- `RenderFrame` in `ChatApp` builds a `VerticalStack` with two entries: `MessageList` (Fill) and `ChatInput` (Content).
- `IChatBackend` does not currently expose `SelectedModelId`. `UrConfiguration.SelectedModelId` is `internal`-accessible only through `UrChatBackend`.

## Structural considerations

**Hierarchy:** `Widget` is the base of all UI chrome; per-side border control belongs there, not scattered into subclasses.

**Abstraction:** `ModelStatusBar` is a leaf widget with no interactivity. One new file, single responsibility.

**Modularization:** The per-side border flags live on `Widget` in `Ur.Terminal`. `ModelStatusBar` goes in `Ur.Tui/Components/` alongside `ChatInput` and `MessageList`.

**Encapsulation:** `IChatBackend` needs a `SelectedModelId` property to keep `ChatApp` from depending on `UrChatBackend` directly.

## Refactoring

- **Replace `bool Border` with per-side flags on `Widget`** — add `BorderTop`, `BorderBottom`, `BorderLeft`, `BorderRight` bool properties. Update `HorizontalChrome`, `VerticalChrome`, `ContentRect`, and the border-drawing path in `Render`. Keep a `Border` convenience setter that sets all four for the ~4 existing box-border call sites.
- **Add `SelectedModelId` to `IChatBackend`** — `UrChatBackend` forwards `host.Configuration.SelectedModelId`; `TestChatBackend` gains the property for tests.

## Implementation plan

### Refactoring

- [ ] On `Widget`, replace `bool Border` with `bool BorderTop`, `bool BorderBottom`, `bool BorderLeft`, `bool BorderRight`
  - Keep a `Border` write-only convenience property that sets all four at once, so existing call sites (`ModelPickerModal`, `ApiKeyModal`, `ExtensionManagerModal`) need no changes
  - `HorizontalChrome`: `(BorderLeft ? 1 : 0) + (BorderRight ? 1 : 0)`
  - `VerticalChrome`: `(BorderTop ? 1 : 0) + (BorderBottom ? 1 : 0)`
  - `ContentRect`: inset each side only if its flag is set
  - `Render`: draw each edge independently (top rule, bottom rule, left pipe, right pipe, corners only where adjacent sides both active)
- [ ] Update `ChatInput` constructor: replace `Border = true` with `BorderTop = true; BorderBottom = true`
- [ ] Add `string? SelectedModelId { get; }` to `IChatBackend`
- [ ] Implement `SelectedModelId` on `UrChatBackend` (delegate to `host.Configuration.SelectedModelId`)
- [ ] Add `SelectedModelId` to `TestChatBackend` with a settable property

### Feature

- [ ] Create `Ur.Tui/Components/ModelStatusBar.cs` — a `Widget` subclass
  - Constructor takes `IChatBackend backend` (or just `string? modelId` — see open question)
  - `MeasureContentHeight` returns `1`
  - `RenderContent` right-aligns `backend.SelectedModelId ?? ""` in the available width using a dim foreground color
  - `HandleKey` returns `false` (not interactive)
- [ ] In `ChatApp`, instantiate `_modelStatusBar = new ModelStatusBar(_backend)` alongside `_chatInput`
- [ ] Update `RenderFrame` to add `ModelStatusBar` as a third `VerticalStack` entry with `SizeConstraint.Content`

## Validation

- **Tests:** Update `Ur.Tui.Tests` — `ChatAppTests` and any snapshot/render tests — to account for the extra status bar row in height calculations. Add a focused unit test for `ModelStatusBar` that verifies right-aligned output for a known model ID.
- **Build:** `dotnet build` — no warnings.
- **Manual:** Run the TUI, confirm input renders with only top/bottom rules, no side pipes. Confirm status bar shows the active model ID right-aligned beneath the input. Change model via `/model` and confirm status bar updates on the next frame.

## Open questions

- Should `ModelStatusBar` hold a reference to `IChatBackend` and read `SelectedModelId` each frame, or should `ChatApp` pass a `string?` prop each render? Reading from `_backend` each frame is simpler and avoids stale state — lean that way unless there's a reason to decouple.
- Corner characters (`┌┐└┘`) only make sense when two adjacent sides are both active. When only top+bottom are active, the ends of the horizontal rules should be plain `─` (no corners). Confirm this is the desired look before implementing.
