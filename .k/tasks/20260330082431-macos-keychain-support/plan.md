# macOS Keychain Support

## Goal

Add a `MacOSKeyring` implementation of `IKeyring` that uses the macOS `security` CLI, and wire up platform detection so the CLI picks the right implementation at startup.

## Desired outcome

`Ur.Cli` runs on macOS using the system Keychain for API key storage, with the same `IKeyring` interface the rest of the codebase already depends on.

## Related code

- `Ur/Configuration/Keyring/IKeyring.cs` — the interface to implement
- `Ur/Configuration/Keyring/LinuxKeyring.cs` — existing implementation, reference for conventions
- `Ur.Cli/Program.cs` — currently hardcodes `new LinuxKeyring()`; needs platform selection
- `Ur.Tests/LinuxKeyringTests.cs` — test structure to mirror for macOS
- `docs/decisions/adr-0008-libraryimport-keyring.md` — established the `IKeyring` approach; notes macOS as follow-up
- `docs/configuration.md` — documents the keyring design

## Current state

- `IKeyring` defines three operations: `GetSecret`, `SetSecret`, `DeleteSecret`.
- `LinuxKeyring` implements via LibraryImport P/Invoke into libsecret. ~200 lines.
- `Program.cs` hardcodes `new LinuxKeyring()` — no platform detection.
- ADR-0008 anticipated macOS: "macOS implementation will follow the same pattern against `Security.framework`." We're deviating from that — the `security` CLI is simpler and equally reliable on macOS.

## Approach

Shell out to the macOS `security` CLI (`/usr/bin/security`) instead of P/Invoking Security.framework.

**Why this deviates from ADR-0008's expectation:** The ADR's Option 3 cons (brittle output, tool may not be installed) don't apply to macOS. `security` ships with every macOS install. The `-w` flag returns just the password with no parsing. Three `Process.Start` calls replace ~150 lines of CoreFoundation/Security.framework interop.

**Key commands:**
- Lookup: `security find-generic-password -s <service> -a <account> -w`
- Store: `security add-generic-password -s <service> -a <account> -w <secret> -U` (`-U` = upsert)
- Delete: `security delete-generic-password -s <service> -a <account>`

**Error handling:**
- Lookup returns exit code 44 when not found → return `null`
- Delete on a nonexistent item returns exit code 44 → treat as success (idempotent)
- Any other non-zero exit code → throw `InvalidOperationException` with stderr

**Platform selection in CLI:**
- Use `RuntimeInformation.IsOSPlatform()` to pick `MacOSKeyring` or `LinuxKeyring`
- Keep selection in `Program.cs` — the host chooses the platform-specific `IKeyring`, per ADR-0011

## Implementation plan

- [x] Add `Ur/Configuration/Keyring/MacOSKeyring.cs`
  - Implement `IKeyring` by shelling out to `/usr/bin/security`
  - Private helper to run a process and capture stdout/stderr/exit code
  - `GetSecret`: `find-generic-password -s -a -w`, return null on exit 44
  - `SetSecret`: `add-generic-password -s -a -w -U`
  - `DeleteSecret`: `delete-generic-password -s -a`, ignore exit 44
- [x] Update `Ur.Cli/Program.cs` to select keyring by platform
  - `RuntimeInformation.IsOSPlatform(OSPlatform.OSX)` → `MacOSKeyring`
  - `RuntimeInformation.IsOSPlatform(OSPlatform.Linux)` → `LinuxKeyring`
  - Throw on unsupported platform
- [x] Add `Ur.Tests/MacOSKeyringTests.cs`
  - Mirror the `LinuxKeyringTests` structure: round-trip, not-found, delete-then-lookup, overwrite
  - These are integration tests — they hit the real macOS Keychain
- [x] Update `docs/decisions/adr-0008-libraryimport-keyring.md`
  - Add a note that macOS uses the `security` CLI rather than Security.framework P/Invoke, and why

## Validation

- **Tests:** Run `MacOSKeyringTests` on macOS — round-trip, not-found, delete, overwrite
- **Manual:** `dotnet run --project Ur.Cli` on macOS — should resolve API key from Keychain and complete the test message
- **Linux:** Confirm `LinuxKeyring` path still works (no regressions from platform selection change)

## Open questions

- Should `MacOSKeyring` target a specific keychain (login vs system), or let `security` use its default search list? Default search list is almost certainly correct — login keychain is where user credentials live.
