# ADR-0005: Scoped Permission Restrictions

- **Status:** accepted
- **Date:** 2026-03-28

## Context and Problem Statement

When an extension attempts a sensitive operation, the user is prompted for approval. The user can grant permission for different scopes: just this once, for this session, for this workspace, or always. But some operations are dangerous enough that broad scopes should not be available — granting blanket approval for network access or out-of-workspace file writes would effectively disable the sandbox.

Relevant docs: [Permission System](../permission-system.md)

## Decision Drivers

- Security — prevent users from accidentally creating permanent security holes
- Usability — minimize prompt fatigue for routine operations
- Intentionality — dangerous approvals should require deliberate, repeated consent
- Simplicity — the scope rules should be easy to understand and remember

## Considered Options

### Option 1: All scopes available for all operations

**Description:** Every permission prompt offers the full range: once, session, workspace, always.

**Pros:**

- Simplest model — one set of options everywhere
- Maximum user control — users decide their own risk tolerance

**Cons:**

- "Always allow network access" effectively disables the sandbox for all current and future extensions
- "Always allow writes outside workspace" is similarly dangerous
- Users experiencing prompt fatigue will click the broadest scope to make the prompts stop
- A single broad grant undermines the entire security model

**When this is the right choice:** When all extensions are fully trusted (e.g. only system extensions, no third-party code).

### Option 2: Restricted scopes for dangerous operations

**Description:** Scope availability depends on the operation type:

| Operation                      | Available scopes                 |
| ------------------------------ | -------------------------------- |
| File read (in workspace)       | Always allowed — no prompt       |
| File write (in workspace)      | Once, Session, Workspace, Always |
| File read (outside workspace)  | Once, No                         |
| File write (outside workspace) | Once, No                         |
| Network access                 | Once, No                         |

**Pros:**

- Dangerous operations cannot be blanket-approved
- In-workspace writes get the full scope range (the user chose to run Ur here)
- The restriction is the security invariant — it cannot be weakened by user behavior

**Cons:**

- Extensions that frequently access the network or write outside the workspace generate many prompts
- Users may find the restrictions frustrating when they trust an extension
- More complex to explain than "all scopes everywhere"

**When this is the right choice:** When extensions come from untrusted sources and the security model must hold even if users are impatient.

### Option 3: Trust-tier-based scope availability

**Description:** System extensions get broader scopes, workspace extensions get restricted scopes.

**Pros:**

- System extensions (shipped by Ur) can operate with less friction
- Workspace extensions (untrusted) are more restricted

**Cons:**

- The security model depends on the extension tier, adding a dimension of complexity
- A compromised system extension gets broad permissions
- User extensions are in an ambiguous middle ground

**When this is the right choice:** When the trust tiers are well-established and system extensions are genuinely trusted.

## Decision

We chose **restricted scopes for dangerous operations** (Option 2) because the security guarantee must hold regardless of user behavior. The key insight is that prompt fatigue is real — if "always allow network" is an option, someone will click it just to stop the prompts, and at that point the sandbox is theater. By making "once" the only option for dangerous operations, each approval is a deliberate act.

In-workspace operations get the full scope range because the user has already made a trust decision by running Ur in that directory. The workspace boundary is the natural trust boundary.

## Consequences

### Positive

- The sandbox cannot be permanently disabled by a single hasty click
- Each network request and out-of-workspace write is a deliberate approval
- The security model is simple to state: "dangerous stuff needs per-use approval"

### Negative

- Extensions that legitimately need frequent network access (e.g. a web search tool) will generate many prompts. This is intentional friction, but it may push extension authors to batch operations or cache aggressively.
- Users who fully trust an extension have no way to suppress prompts for dangerous operations. This is a feature, not a bug, but it will frustrate some users.

### Neutral

- Extension authors are incentivized to stay within the workspace when possible, reducing the blast radius of bugs
- The permission prompt must clearly indicate why scope options are restricted (so users understand they are not broken)

## Confirmation

- No reports of users blanket-approving dangerous operations (because they can't)
- Extension authors adapt by batching network calls or caching results
- Users understand why "always" is not available for some prompts — the UI explains it clearly
- If prompt fatigue becomes a real problem for legitimate use cases, revisit with per-extension trust escalation rather than removing scope restrictions
