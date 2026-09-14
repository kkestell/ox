# Auto-approval mode

## Goal

Let a user choose a session mode that has the full code tool set and executes
approval-gated calls without sending ACP permission requests.

## Desired outcome

Ox advertises `Auto` beside `Code` and `Plan`. A turn started in auto mode may
run every tool available in code mode, including parent and child-agent calls,
without an Ox permission prompt. Workspace confinement, read evidence,
validation, cancellation, output bounds, and executor selection remain
unchanged.

## Summary of approach

Extend the persisted session-mode selection with `auto`. Treat the frozen turn
mode as the execution policy: plan restricts the tool catalog, code applies the
ordinary permission gate, and auto proceeds past that gate without fabricating a
permission request, decision, or reusable grant. Use one small policy helper
from the primary and child tool loops so their behavior cannot drift.

A mode change continues to affect only later turns. A code-mode turn recovered
while waiting for permission therefore reissues its original request even if the
session was subsequently configured for auto mode.

## Related code

- `internal/agent/config_options.go` - Advertises, validates, applies, and
  persists session-mode selections.
- `internal/agent/loop.go` - Owns durable primary-agent approval and tool
  dispatch.
- `internal/agent/subagent.go` - Applies the same approval policy to child tool
  calls.
- `internal/agent/state.go` - Validates persisted selections and frozen request
  configurations.
- `internal/agent/config_options_test.go` and `internal/agent/approval_test.go`
  - Cover option behavior and the production approval path.
- `internal/e2e` - Proves ACP-visible mode, permission, execution, persistence,
  and recovery behavior.
- `docs/spec.md` and `eng/architecture.md` - Own the product contract and the
  durable permission boundary.

## Current state

- Code mode exposes the full tool set and asks for permission when a tool is
  registered with `ApprovalAsk`; plan mode filters the tool set.
- Mode selections and each turn's effective configuration are durable, and a
  running turn uses a frozen configuration.
- Primary calls have durable permission-request recovery. Child calls use a
  separate, non-durable loop but share the parent's frozen configuration and
  session grants.
- Gamma's plan/act/yolo policy confirms the useful prior-art boundary: decide
  whether a call proceeds, prompts, or is forbidden before entering approval
  mechanics. Ox keeps its ACP configuration-option and durable-session design.

## Structural considerations

- **Hierarchy:** Mode interpretation remains in `internal/agent`; tools retain
  their static approval classification and ACP remains only the presentation
  boundary for requests that are actually made.
- **Abstraction:** A focused predicate names whether the frozen mode and tool
  classification require a client decision. It does not introduce a general
  policy framework.
- **Modularization:** Configuration, durable approval, and child execution stay
  in their existing files and packages.
- **Encapsulation:** Auto mode does not mutate tool registrations or session
  grants, so selecting it cannot leak authority into a later code-mode turn.
- **Testability:** The policy is covered directly, while process tests prove no
  permission callback is emitted before representative side effects execute.

## Test plan

- **Key behaviors to verify:** Auto is advertised and persisted; it exposes the
  same tools as code; approval-gated primary, child, web, and MCP calls do not
  request permission; returning to code restores prompts; plan remains
  restricted; switching modes affects only later turns.
- **Test levels:** Focused configuration and approval tests, plus end-to-end
  tests for ACP-visible execution, subagents, session reload, and a recovered
  code-mode permission wait.
- **Edge cases and failure modes:** Auto mode creates no allow-always grant;
  cancellation before dispatch still prevents execution; unknown modes remain
  invalid; persisted auto selections validate; a pending code permission is not
  silently approved by a later configuration change.
- **What not to test:** Repeat confinement, read-evidence, output-bound, and
  executor-conformance cases whose behavior is below the approval policy and is
  unchanged.

## Implementation plan

- Add `auto` to mode constants, configuration options, selection application,
  persisted-state validation, and focused configuration coverage.
- Express the mode-aware permission decision once and use it in both primary and
  child tool execution without creating permission records or grants for
  automatically authorized calls.
- Add end-to-end coverage for automatic parent and child execution, switching
  back to code, persistence, and pending-permission recovery.
- Update the specification, architecture, README, and roadmap after the behavior
  is implemented and verified.

## Documentation updates

- Update `docs/spec.md` with the three mode contracts, turn-boundary switching,
  durable recovery, and the fact that auto authorization is not a session grant.
- Update `eng/architecture.md` so its permission boundary includes explicit
  auto-mode authorization.
- Update `README.md` to advertise the third mode and mark the roadmap item
  complete when all gates pass.

## Impact assessment

- Code paths affected: Session configuration, persisted validation, primary and
  child approval gates, replay/recovery tests, and user-facing documentation.
- Data, protocol, or schema impact: The existing persisted mode string and ACP
  select option gain one value; no new method, field, or storage format is
  introduced.
- Dependency or API impact: No dependency change. ACP clients that render
  configuration options receive one additional mode choice.

## Validation

- Tests to write and run: Focused agent tests while iterating, then
  `make check`.
- Static checks: The checks included by `make check`.
- Manual verification: Inspect the ACP option list and confirm an auto-mode
  mutating call executes without `session/request_permission`.
