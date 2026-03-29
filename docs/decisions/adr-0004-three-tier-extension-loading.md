# ADR-0004: Three-Tier Extension Loading

- **Status:** accepted
- **Date:** 2026-03-28

## Context and Problem Statement

Extensions come from different sources with different trust levels. Some are shipped with Ur, some are installed by the user, and some live in a project repository and could have been contributed by anyone. The extension loading system needs to handle these different trust levels without requiring manual configuration for the common case.

Relevant docs: [Extension System](../extension-system.md), [Permission System](../permission-system.md)

## Decision Drivers

- Security — untrusted extensions must not activate silently
- Convenience — trusted extensions should just work without explicit enabling
- Supply-chain safety — `git pull` should not silently activate new agent capabilities
- Simplicity — the trust model should be easy to understand and explain

## Considered Options

### Option 1: Single directory, all extensions equal

**Description:** All extensions go in one directory (e.g. `~/.ur/extensions/`). All are enabled by default.

**Pros:**

- Simplest model — one directory, one rule

**Cons:**

- No trust differentiation — workspace extensions (from repos) get the same trust as user-installed extensions
- A malicious workspace extension activates on `git pull`

**When this is the right choice:** When all extensions come from trusted sources (e.g. a locked-down corporate environment).

### Option 2: Two tiers (global + workspace)

**Description:** Global extensions in `~/.ur/extensions/` (enabled by default) and workspace extensions in `$WORKSPACE/.ur/extensions/` (disabled by default).

**Pros:**

- Captures the key trust boundary (your stuff vs. repo stuff)
- Simpler than three tiers

**Cons:**

- No distinction between Ur-shipped extensions and user-installed ones
- If Ur ships default extensions, they are mixed with user-installed ones

**When this is the right choice:** When Ur does not ship its own extensions, or when the distinction between "shipped" and "user-installed" does not matter.

### Option 3: Three tiers (system + user + workspace)

**Description:** Three directories with different trust defaults:

- `~/.ur/extensions/system/` — shipped with or blessed by Ur. Enabled by default.
- `~/.ur/extensions/user/` — installed by the user. Enabled by default.
- `$WORKSPACE/.ur/extensions/` — from the project repo. Disabled by default.

**Pros:**

- Clear trust hierarchy: Ur's own extensions, the user's chosen extensions, and repo-provided extensions are distinct
- Workspace extensions require explicit opt-in — `git pull` never silently activates capabilities
- System extensions can be updated/managed separately from user extensions

**Cons:**

- Three directories to understand
- Name collision rules needed (what if system and workspace both have a "git" extension?)

**When this is the right choice:** When Ur ships its own extensions and the trust boundary between repo-provided and user-chosen extensions matters.

## Decision

We chose **three tiers** because the trust boundaries are real and the consequences of getting them wrong are severe. The key insight is that workspace extensions live in the repo — they can be added by any contributor, arrive via `git pull`, and the user may not even notice. Making them disabled by default is a critical security property. Separating system from user extensions is lower stakes but still useful: it lets Ur ship default extensions that the user can't accidentally delete, and lets the user manage their own extensions without touching system ones.

## Consequences

### Positive

- `git pull` never silently activates new extensions — workspace extensions require explicit opt-in
- Ur can ship default extensions (e.g. built-in tools) in the system tier
- Users have a clear mental model: "my stuff" (user), "the project's stuff" (workspace), "Ur's stuff" (system)

### Negative

- Three directories and two different default states (enabled vs. disabled) to explain
- Name collision handling needed — decided: higher-trust tier wins (system > user > workspace), lower-tier extension skipped with warning

### Neutral

- Loading order is deterministic: system → user → workspace. Within a tier, order is unspecified.
- Extension management commands need to understand tiers (`ur extension enable <name>` for workspace extensions)

## Confirmation

- No user reports of surprise extension activation after `git pull`
- Users understand the tier model without reading docs — test with new users
- The system tier is actually used for shipped extensions (if it is always empty, the tier adds complexity for no benefit)
