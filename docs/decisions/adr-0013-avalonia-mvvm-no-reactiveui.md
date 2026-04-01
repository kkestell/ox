# ADR-0013: Avalonia MVVM without ReactiveUI

- **Status:** accepted
- **Date:** 2026-03-31
- **Decision makers:** Kyle Kestell

## Context and Problem Statement

Ur.Gui is a new AvaloniaUI desktop frontend for the Ur library. See [gui.md](../gui.md). AvaloniaUI supports multiple MVVM approaches: plain `INotifyPropertyChanged`, CommunityToolkit.Mvvm source generators, or ReactiveUI (the framework most associated with Avalonia in the community).

The primary driver for the choice is streaming assistant responses: each response chunk must append to a mutable message and trigger a UI update, potentially hundreds of times per second. The decision shapes what the ViewModel layer looks like across the entire application.

## Decision Drivers

- AoT compatibility: the Ur binary is AoT-compiled; Ur.Gui must also support AoT publication. Reflection-heavy frameworks are ruled out.
- Single-developer simplicity: every dependency must earn its keep. A framework that solves problems we don't have yet is a liability.
- No sync-over-async: must not use `.GetAwaiter().GetResult()` anywhere; async patterns must flow naturally through the chosen approach.
- The streaming model is simple: append a string chunk, raise `PropertyChanged`, flip a bool when done. This does not require Rx.

## Considered Options

### Option 1: Plain INPC

**Description:** ViewModels implement `INotifyPropertyChanged` manually. `ObservableCollection<T>` for lists.

**Pros:**
- Zero dependencies beyond Avalonia itself.
- Fully AoT-compatible.
- Easy to understand; no framework knowledge required.

**Cons:**
- Boilerplate: each bindable property requires a backing field, getter, setter, and `OnPropertyChanged` call.
- No composition primitives for derived properties.

**When this is the right choice:** Small projects, or as a baseline before deciding more is needed.

### Option 2: CommunityToolkit.Mvvm (source generators)

**Description:** Source generators emit the INPC boilerplate from `[ObservableProperty]` attributes. The runtime surface is still plain INPC — the generator just eliminates the repetitive code.

**Pros:**
- Eliminates property boilerplate via `[ObservableProperty]`.
- AoT-compatible (source generators run at build time, not runtime).
- No new runtime concepts — the generated code is standard INPC.
- `[RelayCommand]` simplifies `ICommand` implementations.

**Cons:**
- Adds a NuGet dependency (though it is a well-maintained Microsoft package).
- Source generator output is less visible than hand-written code.

**When this is the right choice:** When INPC boilerplate is a real friction point but Rx is not needed.

### Option 3: ReactiveUI

**Description:** Avalonia's most commonly referenced MVVM framework. Models bindings and derived state as `IObservable<T>`. Streams of events and properties are composed with Rx operators.

**Pros:**
- Powerful composition for derived state (e.g., computed properties from multiple sources).
- Streaming events naturally modeled as `IObservable<T>`.
- Strong community examples with Avalonia.

**Cons:**
- Significant learning surface: Rx semantics, `WhenAnyValue`, `ToProperty`, `ObservableAsPropertyHelper` are all new concepts.
- AoT compatibility is uncertain — Rx has historically used reflection internally.
- The streaming model here is simple enough (StringBuilder append + INPC) that Rx adds no expressive benefit for the initial spike.
- Adds a large dependency graph.

**When this is the right choice:** Applications with complex reactive derived state, multiple event sources that must be composed, or teams already fluent in Rx.

## Decision

We chose **CommunityToolkit.Mvvm** (Option 2) as the baseline. Plain INPC is acceptable if the toolkit adds friction; ReactiveUI is off the table for the initial implementation.

The streaming case (`AssistantMessageViewModel.Append`) is handled by a mutable `StringBuilder` backing a computed `Text` property — the ViewModel raises `PropertyChanged` on each append. This is simple, AoT-safe, and requires no Rx.

## Consequences

### Positive

- AoT-compatible from the start.
- Minimal dependency surface.
- No framework knowledge required to read the ViewModel code.

### Negative

- If the GUI gains complex derived state (token counts, cost estimates computed from message history), that logic will require manual INPC chains rather than Rx operators. Acceptable for now.
- If a second GUI frontend is added later and ViewModel sharing becomes a goal, the choice of framework will need to be consistent across both.

### Neutral

- The community's Avalonia examples often use ReactiveUI; some docs and templates will not apply directly.

## Confirmation

The decision is working if ViewModels remain readable without Rx knowledge and the streaming performance is acceptable (no jank at 50+ chunks/second). Revisit if derived state becomes a significant source of bugs or boilerplate.
