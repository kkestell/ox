# Form Questions

## Sources

- `docs/spec.md#todo-and-questions` and `docs/spec.md#session-configuration` —
  question behavior, durability, cancellation, capability gating, and plan-mode
  availability
- `eng/roadmap.md#form-questions` — slice scope and completion gates
- `eng/architecture.md#session-and-turn-state` and
  `eng/architecture.md#protocol-boundary` — frozen turn configuration, callback
  cancellation, and durable tool-result ownership
- `~/src/references/repos/third-party/protocol/agent-client-protocol/schema/v1/schema.json#/$defs/CreateElicitationRequest`,
  `#/$defs/CreateElicitationResponse`, and `#/$defs/ClientCapabilities` — pinned
  form capability, request, scope, schema, and response wire contract
- `~/src/references/repos/personal/kappa/taikonaut/ask_user_tools.py` — model
  tool-to-client-question adapter and structured outcome shape to adapt
- `internal/agent/{agent,loop,subagent,state,tool}.go`, `internal/acp`, and
  `internal/tools` — current capability negotiation, callback, dispatch,
  recovery, wire-type, and built-in-tool seams

## Goal

Add a `question` tool that asks one non-sensitive free-text or single-choice
question through ACP form elicitation. Expose it only when the client explicitly
advertises form support, and preserve each response or interruption as a durable
tool outcome before model execution continues.

## Implementation

- `internal/acp` — add the explicit form capability objects,
  `elicitation/create` method constant, and the narrow request/response types
  needed by Ox. Represent the flattened session and tool-call scope, the
  one-property string schema, titled enum choices, optional default, and the
  `accept`, `decline`, and `cancel` actions. Validate accepted content against
  the requested `answer` field rather than trusting client-side validation;
  reject unknown actions, missing or non-string answers, extra fields, blank
  free-text answers, and choice values outside the declared enum.
- `internal/tools` — register an approval-free, serialized `question` tool for
  parent and child agents in both code and plan modes. Its arguments contain a
  nonempty question, optional labeled choices with descriptions, and an optional
  default. Reject blank or duplicate labels and defaults that do not match the
  applicable free-text or choice contract. Render choices as ACP `oneOf`
  entries, scope the request to the session and current tool call, and return
  compact JSON results that distinguish `accepted` plus its answer, `declined`,
  and `cancelled`. The declaration and system instructions must prohibit
  credentials, secrets, authorization, and execution-permission questions; this
  tool never substitutes for the permission callback.
- `internal/agent/{agent,tool,loop,subagent}.go` — retain whether form support
  was explicitly negotiated and filter both parent and child tool declarations
  at activation. Derive tool kinds and plan-mode declarations from the same
  filtered sets so hidden calls cannot bypass capability absence. Add a
  cancellable client callback for exactly `elicitation/create`, pass it through
  parent and child invocations, and unmarshal its response before tool-level
  schema validation. Do not add a private question method, URL mode, or
  `elicitation/complete`.
- `internal/agent/state.go` — use ordinary durable tool-start and
  tool-completion records for all returned question outcomes. When activation
  finds an unfinished started `question` call, close it with a known failed
  `question interrupted` result instead of the generic unknown-effect result;
  never reissue the form. Live turn cancellation must cancel the callback wait
  and persist the cancelled tool execution before finishing the turn.

## Tests

- ACP parity tests cover omitted, null, empty, form-only, and URL-only
  capabilities plus the exact free-text and titled-choice request shapes and
  every response action. Validation tests cover missing content despite a
  default, wrong keys/types, extra content, blank text, invalid choice values,
  and unknown actions.
- Tool tests cover argument validation, request scope, choice descriptions,
  valid defaults without automatic submission, and the three distinct JSON
  outcomes. Agent tests prove capability-filtered parent/child and plan-mode
  declarations, serialized same-session questions, and direct elicitation
  without a permission request.
- Fake-model integration and real-process tests exercise accepted, declined,
  cancelled, malformed, callback-error, and interrupted responses. Hold one form
  while cancelling its turn and while completing an unrelated session; restart
  with an unanswered started form and prove replay records interruption without
  sending another `elicitation/create`. Inspect the callback JSON against the
  pinned v1 schema and prove an accepted answer is durable before the next
  provider request.

## Decisions

- Use `answer` as the sole requested-schema property and option labels as their
  wire values. This keeps validation and model-visible results direct while
  still allowing clients to render descriptions through titled `oneOf` entries.
- A client `cancel` action is a successful, explicit tool outcome and lets the
  model decide what to do next. Cancelling the ACP turn is different: it aborts
  the callback and ends the turn.
