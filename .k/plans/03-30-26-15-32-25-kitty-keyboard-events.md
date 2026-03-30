# Add Kitty Keyboard Events To Ur.Terminal

## Goal

Add required Kitty keyboard protocol support to `Ur.Terminal`, including key event types (`press`, `repeat`, `release`), so Ur can decode modifier-aware input and distinguish key lifecycle events.

## Desired outcome

- `AnsiTerminal` enables Kitty keyboard protocol with the `11` flags and disables it during cleanup.
- `KeyParser` understands Kitty keyboard sequences in addition to the current ANSI subset.
- `KeyEvent` carries both modifiers and event type so callers can observe press and release distinctly.
- `Shift+Enter` is detectable at the framework level.
- A temporary `Ur.Kitty` executable exists for interactive validation by displaying parsed key events in real time.
- Up/down arrows work end-to-end in the model selection modal when run in a Kitty-capable terminal.

## How we got here

This revision incorporates concrete scope decisions from follow-up feedback:

- Event types are in scope and should be added to the public input model.
- Kitty should be enabled with the same `11` flags used in the working `ox` reference.
- Detecting `Shift+Enter` is required, but growing/multiline `Ur.Tui` input behavior is out of scope for this plan.
- The model picker modal's arrow-key behavior is a required validation target.
- A temporary `Ur.Kitty` app should be added for manual testing and removed later once confidence is high.

I also checked [Ur.Tui/Components/ModelPickerModal.cs](/Users/kyle/src/ur/Ur.Tui/Components/ModelPickerModal.cs) and [Ur.Tui.Tests/ModelPickerModalTests.cs](/Users/kyle/src/ur/Ur.Tui.Tests/ModelPickerModalTests.cs). The modal already handles `Key.Up` and `Key.Down` correctly in isolated tests, which strongly suggests the current failure is in terminal parsing or routing, not the modal itself.

## Approaches considered

### Option 1

- Summary: Extend the existing `KeyParser` and `KeyEvent` model, keep Kitty terminal-mode management inside `AnsiTerminal`, and add a small throwaway `Ur.Kitty` host app for interactive testing.
- Pros: Fits the current architecture, keeps protocol knowledge in the terminal/input layer, and gives us a practical harness to verify event types and modified keys.
- Cons: Requires a deliberate public input-model change, so tests and call sites that construct `KeyEvent` need coordinated updates.
- Failure modes: If event-type support is bolted on without refactoring the parser shape first, the parser becomes difficult to reason about and partial-sequence handling gets brittle.

### Option 2

- Summary: Keep `KeyEvent` unchanged and infer release behavior elsewhere while still parsing Kitty sequences.
- Pros: Smaller API change.
- Cons: Does not meet the requirement. Press/release semantics would be lossy or implicit.
- Failure modes: Consumer code grows ad hoc workarounds and the protocol support is incomplete from day one.

### Option 3

- Summary: Build a separate experimental parser used only by `Ur.Kitty`, then back-port it later into `Ur.Terminal`.
- Pros: Fastest way to experiment on a scratch path.
- Cons: Duplicates logic and increases the chance that the test app and the real framework drift apart.
- Failure modes: We end up validating the wrong parser.

## Recommended approach

- Why this approach: Option 1 gives us the feature the framework actually needs while preserving the current architectural boundaries. `AnsiTerminal` remains the owner of terminal modes, `KeyReader` remains the byte-buffering loop, `KeyParser` remains the place where protocol knowledge lives, and `Ur.Kitty` becomes a thin consumer of the real implementation rather than a fork.
- Key tradeoffs: The public input contract must expand, so the work includes updating tests and any direct `KeyEvent` construction sites. That is worth it because press/release is now a real feature requirement, not future speculation.

## Related code

- `Ur.Terminal/Input/KeyParser.cs` — Current ANSI-only parser; main protocol work lands here.
- `Ur.Terminal/Input/KeyReader.cs` — Existing pending-byte logic that must continue to preserve incomplete sequences.
- `Ur.Terminal/Input/KeyEvent.cs` — Public event contract that now needs an event-type field.
- `Ur.Terminal/Input/Modifiers.cs` — Ur-side modifier flags that Kitty bits must map into explicitly.
- `Ur.Terminal/Input/Key.cs` — Existing key surface; parser output should stay within this surface unless expansion is intentionally chosen.
- `Ur.Terminal/Terminal/AnsiTerminal.cs` — Terminal lifecycle and escape-sequence ownership.
- `Ur.Terminal/Terminal/ITerminal.cs` — Interface boundary to keep stable if possible.
- `Ur.Terminal.Tests/KeyParserTests.cs` — Existing parser coverage to extend for Kitty keys, modifiers, and event types.
- `Ur.Tui/Program.cs` — Current terminal bootstrap path.
- `Ur.Tui/Components/ModelPickerModal.cs` — Current consumer that should benefit from correct arrow-key parsing.
- `Ur.Tui.Tests/ModelPickerModalTests.cs` — Existing proof that the modal logic itself already handles up/down.
- `Ur.slnx` — Solution file that will need the temporary `Ur.Kitty` project added.
- `/Users/kyle/src/ox/pkg/term/parser.go` — Reference parser for Kitty sequence forms.
- `/Users/kyle/src/ox/pkg/term/terminal.go` — Reference for `\x1b[>11u` enable and `\x1b[<u` disable behavior.

## Current state

- Relevant existing behavior:
  - `KeyParser` only handles single-byte keys and a small ANSI CSI subset.
  - `KeyEvent` is `readonly record struct KeyEvent(Key Key, Modifiers Mods, char? Char)`, so there is no place to carry press/repeat/release.
  - `ModelPickerModal` already reacts correctly to `Key.Up` and `Key.Down` in unit tests.
- Existing patterns to follow:
  - `AnsiTerminal` owns terminal escape sequences and cleanup.
  - `KeyParser` is the single translation point from raw bytes to semantic input events.
  - Projects target `net10.0` with nullable enabled.
- Constraints from the current implementation:
  - Adding event type is a public API change and will ripple through tests and call sites that construct `KeyEvent`.
  - `KeyEvent.Char` is still `char?`, so full Kitty Unicode coverage remains broader than this feature.
  - No `CLAUDE.md` file was present during repo exploration; the design docs and codebase patterns are the effective conventions source.

## Structural considerations

This change still fits cleanly if the responsibilities stay where they already belong:

- Hierarchy: Keep escape-sequence and protocol details in `Ur.Terminal`, not in `Ur.Tui` components.
- Abstraction: `KeyEvent` remains the boundary type, but it now becomes rich enough to represent Kitty event kinds explicitly.
- Modularization: Add a focused `KeyEventType` concept instead of smuggling event lifecycle through modifiers or consumer conventions.
- Encapsulation: `Ur.Kitty` should consume public `Ur.Terminal` APIs and parser output, not duplicate parsing logic locally.

## Refactoring

- [ ] Refactor `KeyParser` CSI handling into smaller helpers before adding Kitty event-type branches.
  This keeps ANSI parsing, Kitty `CSI ... u` parsing, sequence scanning, and modifier/event-type decoding separable and testable.
- [ ] Introduce a dedicated `KeyEventType` enum and extend `KeyEvent` to include it.
  This makes press/repeat/release first-class data rather than an implicit parser policy.
- [ ] Centralize Kitty enable/disable escape emission in `AnsiTerminal` behind private helpers and state flags.
  This keeps lifecycle cleanup idempotent and avoids duplicated literal sequences.

## Research

### Repo findings

- [docs/terminal-framework.md](/Users/kyle/src/ur/docs/terminal-framework.md) already anticipated Kitty support and intentionally kept modifiers in the public input model.
- [Ur.Tui/Components/ModelPickerModal.cs](/Users/kyle/src/ur/Ur.Tui/Components/ModelPickerModal.cs) consumes `Key.Up`/`Key.Down` directly and should not need feature work for basic arrow navigation.
- [Ur.Tui.Tests/ModelPickerModalTests.cs](/Users/kyle/src/ur/Ur.Tui.Tests/ModelPickerModalTests.cs) already covers up/down selection movement, which makes it a good regression suite for the parser work.

### Reference findings

- The `ox` reference enables Kitty with `\x1b[>11u`, which matches the chosen requirement.
- The Kitty parser in `ox` handles `CSI codepoint ; modifiers:event_type u` plus modifier-bearing cursor-key and `~` forms.
- The reference subtracts 1 from Kitty's encoded modifier field, but Ur still needs an explicit translation into `Modifiers.Shift`, `Modifiers.Ctrl`, and `Modifiers.Alt`.

## Implementation plan

- [ ] Add a new input event-kind type in `Ur.Terminal`, for example `KeyEventType` with `Press`, `Repeat`, and `Release`, and extend `KeyEvent` to carry it.
- [ ] Update direct `KeyEvent` construction sites in tests and app code to use the new shape without changing their behavior.
- [ ] Add private Kitty-mode helpers to `AnsiTerminal` and enable Kitty with `\x1b[>11u` as part of terminal setup, then disable it with `\x1b[<u` during cleanup.
- [ ] Refactor `KeyParser.Parse` so it can scan for complete ANSI or Kitty sequences and return `null` for incomplete input instead of consuming partial bytes.
- [ ] Add Kitty `CSI ... u` parsing for the Ur-relevant subset:
  - printable BMP characters representable as `char`
  - Enter, Tab, Escape, Backspace, Space
  - letters and digits with modifiers
  - named keys already represented in `Key`
- [ ] Add modifier parsing for Kitty-reported cursor-key and `~` sequences so arrows, Home/End, Delete, PageUp/PageDown, and supported function-key variants preserve modifier bits.
- [ ] Parse Kitty event-type suffixes and map them directly into the new `KeyEventType` enum instead of dropping release events.
- [ ] Define normalization rules for parser output:
  - map Kitty modifier bits into Ur `Modifiers`
  - preserve existing `Key` mapping behavior where possible
  - populate `Char` only when the key maps to a safe single `char`
- [ ] Expand `Ur.Terminal.Tests/KeyParserTests.cs` with focused coverage for:
  - plain Kitty press events
  - repeat and release events
  - modified printable keys
  - modified arrows/Home/End/Delete/PageUp/PageDown
  - incomplete Kitty sequences
  - unknown or unsupported Kitty values
- [ ] Keep `Ur.Tui` feature scope tight:
  - do not add growing or multiline input behavior
  - do validate that `Shift+Enter` is detectable from the emitted `KeyEvent`
- [ ] Add regression coverage around the model picker path so arrow-key navigation is exercised as an end-to-end validation target after parser changes.
- [ ] Add a temporary `Ur.Kitty` executable project that references `Ur.Terminal`, initializes the terminal, prints parsed `KeyEvent` data to the screen, and is included in `Ur.slnx`.
- [ ] Document that `Ur.Kitty` is a temporary diagnostic app intended to be deleted after Kitty input support is verified.
- [ ] Update terminal docs to describe Kitty support, event types, and any remaining intentional limitations around Unicode or unsupported Kitty-specific keys.

## Impact assessment

- Code paths affected: Terminal setup/cleanup, key parsing, parser tests, direct `KeyEvent` construction sites, solution/project wiring, and manual test tooling.
- Data or schema impact: None.
- Dependency or API impact:
  - Public input API changes because `KeyEvent` gains event-type data.
  - `Ur.Kitty` adds a temporary executable project to the solution.

## Validation

- Tests:
  - Add parser unit tests for Kitty `Press`, `Repeat`, and `Release`.
  - Add tests for Kitty-modified arrows and `~` sequences.
  - Add tests proving incomplete Kitty sequences remain pending.
  - Keep or add model picker tests that verify a parsed down-arrow event changes the selected model.
- Lint/format/typecheck:
  - `dotnet build`
  - `dotnet test`
- Manual verification:
  - Run `Ur.Kitty` inside Kitty or another terminal that supports the Kitty keyboard protocol.
  - Press modified printable keys, arrows, Home/End, Delete, and `Shift+Enter`, and confirm the displayed `KeyEvent` output includes the expected key, modifiers, char, and event type.
  - Hold a key long enough to observe repeat events.
  - Release a modified key and confirm release is emitted.
  - Run `Ur.Tui`, open the model picker, and confirm up/down arrows move the selection reliably.
  - Exit both apps cleanly and verify the terminal is restored even after Ctrl+C.

## Gaps and follow-up

- Gap: Full non-BMP Unicode input is broader than this change and is constrained by the current `char?` event shape and one-cell rendering model.
- Follow-up: Treat richer Unicode keyboard input as a separate design change if Ur needs it.
- Gap: `Shift+Enter` can be detected after this work, but multiline/growing input behavior in `Ur.Tui` remains intentionally out of scope.
- Follow-up: Land the `ChatInput` UX change separately once Kitty input support is proven stable.
- Gap: `Ur.Kitty` is intentionally temporary and should not become a permanent second app by accident.
- Follow-up: Remove the project once manual Kitty validation is no longer needed.

## Open questions

- Do we want `KeyEventType.Press` to be the default for legacy ANSI sequences that do not encode event lifecycle, or should legacy paths use a distinct `Unknown`/`ImplicitPress` value?
- Should the first Kitty implementation continue to limit output to the current `Key` enum, or is this the right moment to add additional Kitty-native named keys such as Insert or numpad variants if they appear during testing?
