# Connect Wizard

## Goal

Replace the current pre-TUI plain-console configuration phase with an in-TUI wizard
that walks the user through provider selection, API key entry, and model selection.
The same wizard is triggered on demand by `/connect`.

## Desired outcome

- On first launch (or when `ur.model` is not set in settings.json), Ox enters the
  TUI immediately and shows the connect wizard overlay instead of chat.
- `/connect` always opens the wizard, regardless of current configuration state.
- The wizard proceeds through three sequential steps: Select Provider → Enter API Key
  (skipped for Ollama) → Select Model.
- Arrow keys navigate lists; Enter confirms and advances; Escape cancels (exits the
  app on first-run when no config exists, otherwise dismisses the wizard).
- On completion, `ur.model` and the provider API key are saved and chat is ready.

## How we got here

The feature description was concrete and well-scoped. No brainstorm was needed.
The main architectural question was how to move first-run configuration out of the
pre-TUI `RunConfigurationPhaseAsync` phase and into the TUI itself, reusing the
same wizard for `/connect`. The recommended approach (below) keeps the pre-TUI
phase for fast failures (e.g. bad providers.json) but removes the interactive
configuration loop, delegating everything to the in-TUI wizard.

## Related code

- `src/Ox/Program.cs` — contains `RunConfigurationPhaseAsync`, the current plain-console
  config phase that this plan replaces.
- `src/Ox/OxApp.cs` — main loop, input routing, render pipeline, permission prompt bridge.
  The wizard follows the same pattern as the permission prompt.
- `src/Ox/Permission/PermissionPromptView.cs` — existing floating modal; models the
  rendering and input-intercept pattern the wizard should follow.
- `src/Ox/Views/InputAreaView.cs` — box-drawing rendering reference.
- `src/Ur/Configuration/UrConfiguration.cs` — exposes `SetSelectedModel`, `SetApiKey`,
  `Readiness`, and `ListAllModelIds`. Will need new provider/model query methods.
- `src/Ur/Providers/IProvider.cs` — already has `RequiresApiKey` property; used to
  skip the API key step for Ollama.
- `src/Ur/Providers/ProviderConfig.cs` — static provider metadata (type, URL, models);
  `ProviderConfigEntry` needs a `name` field added.
- `src/Ur/Skills/BuiltInCommandRegistry.cs` — `/connect` must be registered here.
- `providers.json` — needs a `name` field added to each provider entry.

## Current state

- First-run config is a plain-console loop in `Program.RunConfigurationPhaseAsync`
  that runs before the TUI starts. It reads from `Console.ReadLine()`.
- `IProvider.RequiresApiKey` already exists; `OllamaProvider` returns `false`.
- `ProviderModelEntry` already has a `name` field (display name).
- `ProviderConfigEntry` does **not** have a `name` field; provider display names
  must be added.
- The permission prompt (`PermissionPromptView`) demonstrates the floating modal +
  keyboard-intercept + TCS bridge pattern that the wizard adapts.
- `/connect` is not yet registered as a built-in command.

## Structural considerations

**Hierarchy**: The wizard is purely a UI concern (Ox layer). All config mutations
go through `UrConfiguration` (Ur layer) via the existing public API. No Ur code
reaches into Ox.

**Abstraction**: The wizard state machine (`ConnectWizardController`) lives in Ox
and is separate from the view (`ConnectWizardView`). OxApp owns the controller and
mediates between it and `UrConfiguration`, the same way OxApp mediates between the
permission prompt view and the permission system.

**Modularization**: New code lives in `src/Ox/Connect/`, mirroring the existing
`src/Ox/Permission/` namespace. The controller handles state transitions; the view
handles rendering; OxApp wires them together.

**Encapsulation**: `UrConfiguration` gets three new public methods to satisfy the
wizard's data needs without exposing internal `ProviderConfig` or `ProviderRegistry`
instances. The view and controller never call into Ur directly.

## Refactoring

### 1 — Add `name` to `ProviderConfigEntry` and providers.json

`ProviderConfigEntry` currently has no display-name field. Without it, the wizard
must render raw keys like `"zai-coding"` instead of `"Z.AI Coding Plan"`. Add a
`name` (JSON: `"name"`) field and populate it in providers.json for every provider.

This is a pure additive change to providers.json and its deserializer model; nothing
else reads `ProviderConfigEntry.Name` today.

### 2 — Expose wizard query methods on `UrConfiguration`

The wizard needs three new public methods:

```csharp
// Ordered list of providers for the selection step.
public IReadOnlyList<(string Key, string DisplayName)> ListProviders();

// Models for the chosen provider, for the model-selection step.
public IReadOnlyList<(string Id, string Name)> ListModelsForProvider(string providerKey);

// Whether the chosen provider needs an API key (Ollama does not).
public bool ProviderRequiresApiKey(string providerKey);
```

`ListProviders` delegates to `ProviderConfig.ProviderNames` + `ProviderConfigEntry.Name`.
`ListModelsForProvider` delegates to `ProviderConfig.GetEntry(providerKey)?.Models`.
`ProviderRequiresApiKey` delegates to `ProviderRegistry.Get(providerKey)?.RequiresApiKey ?? true`.

### 3 — Register `/connect` in `BuiltInCommandRegistry`

Add `new("connect")` to the list in `BuiltInCommandRegistry`. Like `/quit`, the
command is intercepted in `OxApp.SubmitInput` before reaching the session layer.

### 4 — Simplify `Program.RunConfigurationPhaseAsync`

Remove the interactive loop. The method becomes a no-op (or is deleted outright),
since the TUI wizard handles first-run setup. Program.cs still validates providers.json
on startup and exits with an error on parse failures — that part stays.

## Implementation plan

- [ ] **providers.json + ProviderConfigEntry**: Add `"name"` JSON field to
  `ProviderConfigEntry`. Populate human-readable display names in providers.json
  for all five providers (Google, Ollama, OpenAI, OpenRouter, Z.AI Coding Plan).

- [ ] **UrConfiguration — `ListProviders()`**: Implement method returning
  `IReadOnlyList<(string Key, string DisplayName)>` in the same order as
  `ProviderConfig.ProviderNames`. Fall back to `Key` if `Name` is null/empty.

- [ ] **UrConfiguration — `ListModelsForProvider()`**: Implement method returning
  `IReadOnlyList<(string Id, string Name)>` for the given provider key, or an
  empty list if the provider is unknown.

- [ ] **UrConfiguration — `ProviderRequiresApiKey()`**: Implement method that
  delegates to `ProviderRegistry.Get(providerKey)?.RequiresApiKey ?? true`.

- [ ] **`src/Ox/Connect/ConnectWizardController.cs`**: State machine for the wizard.
  Owns the current step, the selected provider key, the text editor for API key
  input, the selected item index, and the item lists. Exposes:
  - `bool IsActive`
  - `bool IsRequired` (true when opened due to missing config; Escape exits app)
  - `WizardStep CurrentStep` (enum: SelectProvider, EnterApiKey, SelectModel)
  - `IReadOnlyList<string> DisplayItems` (current step's displayable strings)
  - `int SelectedIndex`
  - `TextEditor KeyEditor`
  - `void Start(IReadOnlyList<(string Key, string Name)> providers, bool required)`
  - `void ProviderConfirmed(string selectedKey, bool requiresApiKey, IReadOnlyList<(string Id, string Name)> models)`
  - `void ApiKeyConfirmed()`
  - `(string ProviderId, string ModelId, string? ApiKey)? ModelConfirmed()` — returns
    final selections (or null on Cancel)
  - `void NavigateUp()`, `void NavigateDown()`
  - `void Cancel()`

- [ ] **`src/Ox/Connect/ConnectWizardView.cs`**: Renders the centered modal overlay.
  Two rendering modes driven by `WizardStep`:
  - **List mode** (SelectProvider, SelectModel): Box with title row, `├───┤` divider,
    then one row per item. The selected item is prefixed with `>`, others with spaces.
  - **Input mode** (EnterApiKey): Box with title row, divider, then a single text
    field row with cursor.
  Width is `max(widest_item + 6, 32)`, centered horizontally. Height is
  `2 (borders) + 1 (title) + 1 (divider) + item_count` for list mode, or
  `2 + 1 + 1 + 1` for input mode. Rendered at vertical center of the screen.
  Box uses `╭─╮ │ ├─┤ ╰─╯` (matching existing views).

- [ ] **OxApp — fields**: Add `ConnectWizardController _wizard` and
  `ConnectWizardView _wizardView` alongside the existing `_permissionPromptView`.

- [ ] **OxApp — constructor**: After creating the session, check
  `!host.Configuration.Readiness.CanRunTurns`. If true, call
  `_wizard.Start(host.Configuration.ListProviders(), required: true)` to open the
  wizard immediately.

- [ ] **OxApp — `HandleKey`**: When `_wizard.IsActive`, route to
  `HandleWizardInput` before any other handler returns. Escape and navigation keys
  are consumed here; the main input area is locked out.

- [ ] **OxApp — `HandleWizardInput`**: Dispatch by `_wizard.CurrentStep`:
  - **SelectProvider / SelectModel**: Up/Down → `NavigateUp/Down`; Enter → advance
    (call `ProviderConfirmed` or `ModelConfirmed`); Escape → `Cancel`.
  - **EnterApiKey**: Printable chars and Backspace → edit `_wizard.KeyEditor`;
    Enter → `ApiKeyConfirmed()`; Escape → `Cancel`.
  After each `Cancel()` call: if `_wizard.IsRequired`, set `_exit = true`; otherwise
  just deactivate the wizard.
  After `ModelConfirmed()`: call `host.Configuration.SetSelectedModel(...)`, and if
  `apiKey` is non-empty call `host.Configuration.SetApiKey(...)`. Also invalidate
  `_contextWindowCache`.

- [ ] **OxApp — `SubmitInput`**: Handle `/connect` like `/quit` — do not delegate
  to the session. Call `_wizard.Start(host.Configuration.ListProviders(), required: false)`.

- [ ] **OxApp — `Render`**: After rendering the input area, if `_wizard.IsActive`,
  call `_wizardView.Render(_buffer, _wizard)` as the final draw pass so the wizard
  appears on top of everything.

- [ ] **BuiltInCommandRegistry**: Add `new("connect")` to the command list.

- [ ] **Program.cs — remove `RunConfigurationPhaseAsync`**: Delete the method and
  remove the call site. The TUI now handles first-run setup. Keep providers.json
  error handling. Remove the `ShowAvailableModels` helper too, as it's no longer needed.

- [ ] **Tests**: Unit-test `ConnectWizardController` covering:
  - Start → NavigateDown twice → Enter → ProviderConfirmed (with and without API key
    requirement) → Enter on API key step → NavigateDown → Enter → ModelConfirmed
    returns expected (provider, model, key).
  - Cancel on SelectProvider with `IsRequired = true` → verify wizard is inactive.
  - Cancel on SelectProvider with `IsRequired = false` → verify wizard is inactive
    and no config mutation.
  - Skip API key step when `ProviderRequiresApiKey` is false.

## Validation

- `dotnet build` — no warnings.
- `dotnet test` — all unit tests pass, including new wizard controller tests.
- Manual: run `ox` with no settings.json model key → wizard appears immediately,
  complete it, chat works.
- Manual: run `/connect` mid-session → wizard appears, changes provider and model,
  status line updates.
- Manual: Escape on first-run wizard → app exits cleanly.
- Manual: Escape on `/connect` wizard → wizard dismisses, existing config preserved.
- Manual: select Ollama → API key step is skipped.
- Manual: select a provider that already has a saved key → API key step still appears
  (field empty; pressing Enter with empty field keeps existing key).

## Open questions

- When the user presses Enter on an empty API key field (provider already has a saved
  key): should Ox (a) keep the existing key unchanged, or (b) clear it? The feature
  spec implies the wizard is how you *change* the key — so (a) seems correct, but
  confirm before implementing.
