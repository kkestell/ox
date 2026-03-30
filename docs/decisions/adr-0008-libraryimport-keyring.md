# ADR-0008: LibraryImport P/Invoke for Cross-Platform Keyring Access

- **Status:** accepted
- **Date:** 2026-03-29

## Context and Problem Statement

Ur stores API keys in the system keyring (never in config files). macOS has Keychain, Linux has libsecret/kwallet. We need a .NET abstraction that works with AoT compilation. No existing .NET library provides cross-platform keyring access with confirmed Native AoT compatibility.

See [Configuration](../configuration.md).

## Decision Drivers

- **AoT compatibility.** No reflection, no dynamic loading. Must work with .NET 10 Native AoT publishing.
- **No external dependencies.** Single-binary deployment — no runtime framework or CLI tool requirements beyond what the OS provides.
- **Small surface area.** Only three operations: store, lookup, delete. The abstraction should match.
- **Single-developer maintainability.** The solution must be small enough to own entirely.

## Considered Options

### Option 1: Existing .NET keyring libraries

**Description:** Use an existing package (KeySharp, NativeCredentialStore).

**Pros:**

- No P/Invoke code to write or maintain.

**Cons:**

- KeySharp: unmaintained, no AoT verification.
- NativeCredentialStore: bundles Docker credential helpers, much larger scope than needed.
- None have confirmed Native AoT compatibility.

**When this is the right choice:** When AoT is not a constraint and the library is actively maintained.

### Option 2: `LibraryImport` P/Invoke wrapper

**Description:** Write ~150 lines of P/Invoke per platform behind an `IKeyring` interface. Use `LibraryImport` (source-generated marshalling) for AoT compatibility.

**Pros:**

- Full AoT compatibility via source-generated marshalling.
- Small, auditable surface area.
- No external dependencies — calls OS-provided libraries directly.
- Complete control over the implementation.

**Cons:**

- P/Invoke code is fiddly (struct padding, pointer lifetime, GHashTable management on Linux).
- Per-platform implementation and testing burden.
- Depends on OS libraries at runtime (`libsecret-1.so.0` + `libglib-2.0.so.0` on Linux).

**When this is the right choice:** When AoT is a hard constraint, the API surface is small, and you need full control.

### Option 3: Shell out to CLI tools

**Description:** Call `security` (macOS) or `secret-tool` (Linux) as subprocesses.

**Pros:**

- No P/Invoke. Simple `Process.Start` calls.
- Works with AoT trivially.

**Cons:**

- Process startup overhead for every secret operation.
- Parsing CLI output is brittle (format changes across versions).
- `secret-tool` may not be installed on all Linux distros.

**When this is the right choice:** For a prototype or when P/Invoke maintenance is not viable.

## Decision

We chose **Option 2: `LibraryImport` P/Invoke** because it satisfies the AoT constraint with a small, fully controlled codebase (~150 lines per platform). The API surface is only three operations (store, lookup, delete), so the P/Invoke complexity is bounded.

Implementation details:

- Uses libsecret's `v` variants (`secret_password_storev_sync`, `lookupv_sync`, `clearv_sync`) — fixed signatures, no varargs, fully `LibraryImport`-compatible.
- GHashTable attribute passing requires careful memory management: `g_hash_table_new_full` with `g_free` as destroy notifier, `g_strdup` for all keys/values.
- `SecretSchema` struct padded to match full 592-byte C layout on x64.
- Function pointers for `g_str_hash`/`g_str_equal` resolved via `NativeLibrary.GetExport`.
- Schema: `xdg:schema=ur.keyring` with `service` and `account` string attributes.

## Consequences

### Positive

- Full AoT compatibility with zero runtime dependencies beyond OS-provided libraries.
- Small, auditable codebase behind a clean `IKeyring` interface.
- Works with any `org.freedesktop.secrets` provider (gnome-keyring-daemon, KWallet portal).

### Negative

- P/Invoke maintenance burden (struct layouts, pointer lifetimes, platform-specific testing).
- Runtime dependency on `libsecret-1.so.0` and `libglib-2.0.so.0` on Linux (standard on GNOME/KDE desktops but not guaranteed on minimal server installs).

### Neutral

- macOS implementation uses the `security` CLI (`/usr/bin/security`) rather than `LibraryImport` P/Invoke into Security.framework. The `security` tool ships with every macOS install, the `-w` flag returns just the password (no output parsing), and three `Process.Start` calls replace ~150 lines of CoreFoundation/Security.framework interop. The ADR's Option 3 cons (brittle output, tool may not be installed) apply to Linux's `secret-tool` but not to macOS's `security`.

## Confirmation

- Spike confirmed on Linux: P/Invoke into `libsecret-1.so.0` works with gnome-keyring-daemon.
- API keys round-trip correctly through store → lookup → delete.
- AoT-published binary accesses the keyring without errors.
