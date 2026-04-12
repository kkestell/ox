# Drop the Fake-Async Pattern from the Settings API

## Goal

Eliminate `GetAwaiter().GetResult()` in `ExecuteModelCommand` by making the settings write
API genuinely synchronous rather than fake-async.

## How we got here

`UrSession.ExecuteModelCommand` calls `_configuration.SetSelectedModelAsync(args).GetAwaiter().GetResult()`.
The existing comment claims this is safe because the code runs on a synchronous `HandleKey` path and the
underlying I/O is a local file write. That reasoning is partly correct, but incomplete:

`SettingsWriter.SetAsync` is **entirely synchronous** — it calls `File.ReadAllText`,
`File.WriteAllText`, and `_configurationRoot.Reload()` (all blocking), then returns
`Task.CompletedTask`. There is no captured `SynchronizationContext` and no real continuation
to schedule, so `.GetAwaiter().GetResult()` happens to not deadlock today. But the `Async`
suffix on every settings method is a lie: it advertises a contract it does not fulfill.

The root cause is the entire `UrConfiguration` / `SettingsWriter` API carrying `Async`
suffixes and `Task` return types for operations that are — and should be — synchronous.
Renaming them makes the code honest and removes the motivation for `.GetAwaiter().GetResult()`
entirely.

## Approaches considered

### Option A — Make the chain genuinely async

Convert `SettingsWriter` to use `File.WriteAllTextAsync` / `ReadAllTextAsync`, propagate
`Task` through `UrConfiguration`, make `ExecuteBuiltInCommand` return `Task<CommandResult?>`,
and restructure `OxApp.SubmitInput` / `HandleKey` to handle an async command result.

- Pros: Correct async all the way up; future-proofs if I/O ever gets slower.
- Cons: `OxApp` is a single-threaded render loop that processes all state mutations on the
  main thread. Making `HandleKey` → `SubmitInput` → `ExecuteBuiltInCommand` async would
  require either fire-and-forget (losing the synchronous response needed to update UI state)
  or restructuring the render loop significantly. Overengineering for local settings files
  that complete in under 1 ms.
- Failure modes: Async state machine overhead everywhere; subtle ordering issues between
  command result display and subsequent render cycles.

### Option B — Strip the fake async; make the API genuinely synchronous (recommended)

Rename `SettingsWriter.SetAsync` → `Set`, `ClearAsync` → `Clear`, return `void`.
Do the same for all `*Async` methods on `UrConfiguration`. Update all callers to drop `await`.
`ExecuteModelCommand` calls `_configuration.SetSelectedModel(args)` directly — no blocking
pattern at all.

- Pros: The code matches what it already does. No `Task` allocation. No deadlock concern
  ever. The `HandleKey` path stays cleanly synchronous, consistent with the rest of the TUI.
- Cons: Every `await config.*Async(...)` call in Program.cs and tests must be updated.
  The public surface shrinks — but this is greenfield with no backwards compat requirement.
- Failure modes: If a future maintainer wants to introduce real async I/O in `SettingsWriter`,
  they must do so explicitly rather than relying on the existing `Task` shape. This is a
  feature, not a bug — the `Async` names gave false reassurance that it was already safe to
  call from async contexts.

## Recommended approach

**Option B.** The operations are inherently synchronous (local JSON file + config reload).
The `Async` suffix was premature. Removing it makes all contracts honest and eliminates the
source of the `.GetAwaiter().GetResult()` pattern permanently.

## Related code

- `src/Ur/Settings/SettingsWriter.cs` — The fake-async source: `SetAsync`/`ClearAsync` both end with `return Task.CompletedTask`.
- `src/Ur/Configuration/UrConfiguration.cs` — All eight public `*Async` write methods delegate to `SettingsWriter` and are fake-async.
- `src/Ur/Sessions/UrSession.cs:390` — `ExecuteModelCommand` contains the `.GetAwaiter().GetResult()` call being fixed.
- `src/Ox/Program.cs:133,139,150` — CLI config phase uses `await config.SetSelectedModelAsync`, `ClearSelectedModelAsync`, `SetApiKeyAsync`.
- `tests/Ur.Tests/ConfigurationTests.cs` — Configuration tests use `await` on all write methods.
- `tests/Ur.Tests/ExecuteBuiltinCommandTests.cs` — Already exercises `ExecuteBuiltInCommand("model", ...)` end-to-end; verifies persistence.

## Structural considerations

**Hierarchy:** `SettingsWriter` is an infrastructure detail; `UrConfiguration` is its domain
façade; `UrSession` is the session layer that consumes the façade. All dependencies flow
downward — this refactor does not change that.

**Abstraction:** Stripping `Task` from purely synchronous methods moves them to the correct
level of abstraction. A method that reads a JSON file, mutates it, and writes it back is not
meaningfully asynchronous. The fake-async wrapper added abstraction without adding value.

**Encapsulation:** `SettingsWriter.Set/Clear` remain `internal sealed` — nothing about their
visibility changes. `UrConfiguration`'s public surface becomes simpler (void returns vs Task
returns).

**SOLID:** This is a single-responsibility cleanup. Each method does one thing; returning
`Task.CompletedTask` from an inherently synchronous method violated the principle that
abstractions should not lie.

## Implementation plan

### Refactoring (prerequisite)

- [x] In `SettingsWriter`, rename `SetAsync` → `Set` and `ClearAsync` → `Clear`. Change return
  types from `Task` to `void`. Remove `return Task.CompletedTask` from both methods.
- [x] In `UrConfiguration`, rename all eight `*Async` write methods to their non-async
  equivalents and change return types to `void`:
  - `SetSelectedModelAsync` → `SetSelectedModel`
  - `ClearSelectedModelAsync` → `ClearSelectedModel`
  - `SetApiKeyAsync` → `SetApiKey`
  - `ClearApiKeyAsync` → `ClearApiKey`
  - `SetSettingAsync` → `SetSetting`
  - `SetStringSettingAsync` → `SetStringSetting`
  - `SetBoolSettingAsync` → `SetBoolSetting`
  - `ClearSettingAsync` → `ClearSetting`

### Bug fix

- [x] In `UrSession.ExecuteModelCommand`, replace:
  ```csharp
  _configuration.SetSelectedModelAsync(args).GetAwaiter().GetResult();
  ```
  with:
  ```csharp
  _configuration.SetSelectedModel(args);
  ```
  Update the XML doc comment to remove the now-stale paragraph about `GetAwaiter().GetResult()`.

### Caller updates

- [x] In `Program.cs` (`RunConfigurationPhaseAsync`), replace all `await config.*Async(...)`
  calls with their synchronous equivalents: `config.SetSelectedModel(...)`,
  `config.ClearSelectedModel(...)`, `config.SetApiKey(...)`.
- [x] In `tests/Ur.Tests/ConfigurationTests.cs`, remove `await` from all write-method calls
  and update method signatures if they are now purely synchronous (drop `async Task` where
  there are no remaining `await` expressions).
- [x] In `tests/Ur.Tests/SettingsLoaderTests.cs` (and any other test file that calls
  `SettingsWriter.SetAsync`/`ClearAsync` directly), update to the renamed sync methods.
  The review found these: `SkillSessionTests`, `HostSessionApiTests`,
  `MultiProviderSmokeTests`, `PermissionTests`, `SettingsLoaderTests`.
- [x] Search the entire solution for remaining references to the old `*Async` names (Grep for
  `SetSelectedModelAsync`, `SetApiKeyAsync`, `ClearSelectedModelAsync`, `ClearApiKeyAsync`,
  `SetSettingAsync`, `SetStringSettingAsync`, `SetBoolSettingAsync`, `ClearSettingAsync`,
  `\.SetAsync\(`, `\.ClearAsync\(`) and update any stragglers.

## Validation

- Run the full test suite (`dotnet test`) and verify all tests pass. The existing
  `ExecuteBuiltinCommandTests` already covers the `/model` command end-to-end including
  persistence — no new tests are needed for the happy path.
- Confirm the solution builds with no warnings (`dotnet build -warnaserror`).
- Grep for `.GetAwaiter().GetResult()` in `UrSession.cs` to confirm it is gone.
- Grep for the old `*Async` names across `src/` to confirm no dead references remain.
- Manual: start the TUI with `--fake-provider`, type `/model fake/fast`, press Enter,
  verify the status line updates and the setting persists across restart.
