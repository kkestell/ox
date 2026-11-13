# oxd relays form elicitations from agents to clients

- Task: `2026-11-12-012-elicitation-relay-in-oxd`
- Kind: plan
- Status: ready
- Source HEAD: `82203839c1c7243c204a02f86aa0324dbe0bb515`
- Dependencies: none

## Outcome and boundaries

oxd tells every agent that it supports form elicitation. When an agent sends
`elicitation/create` (form mode, session scope), oxd records it as a session
event, the session reads `awaiting_input`, any client can answer it with
`elicitation.respond`, and the answer goes back to the agent. The request is
stored with the session's events and replayed to subscribers. When the agent
withdraws it with `$/cancel_request`, exits, or the session parks or closes, it
resolves as `cancel`. `oxctl attach` prints elicitations and `oxctl answer`
answers one. `oxd-fake-agent` gets an `elicit` directive that later units' tests
use.

Non-goals: URL-mode elicitation, `elicitation/complete`, the bridge (unit
`2026-11-12-013`), the app (unit `2026-11-12-014`).

The project speaks ACP v2 (`unstable_protocol_v2`; Cargo.lock resolves
`agent-client-protocol-schema` 1.17.0), so the types are
`agent_client_protocol::v2::schema::{CreateElicitationRequest, ElicitationSchema,
ElicitationAction, ElicitationCapabilities, ...}`, not the v1 module of 1.9.1.
The wire shape is the same.

## Current system

- `crates/oxd/src/session.rs` `Session::run`: sends `InitializeRequest` with no
  client capabilities; handles `RequestPermissionRequest` by recording
  `Change::PermissionRequested { request, answer }` and responding from the
  oneshot, `Cancelled` if the sender drops. `Session::permit` →
  `State::request(Request::Permit)`.
- `crates/oxd/src/state.rs`: `Live.pending: Vec<Pending>` (permission only);
  `Live::state` gives `AwaitingPermission` while any is pending; `quiet`,
  `cancel_pending` (on `Change::Cancelled`), `Live::end` (exit, park, close),
  `State::fill` (cancels requests a crash left open, continues `next_request`).
  Request IDs come from `next_request`.
- `crates/ox-protocol/src/messages.rs`: `Method::PermissionRespond`,
  `SessionState`, `EventKind::{PermissionRequested, PermissionResolved}`;
  `client.rs` `Client::permit`; `crates/oxd/src/daemon.rs` dispatches it.
- `crates/oxctl/src/main.rs`: `Command::Permit`, `describe` for events.
- `crates/oxd/src/bin/oxd-fake-agent.rs`: `Directive::Permission` in `run`.
- ACP v2 elicitation spec: `session/cancel` does not cancel elicitations; the
  agent withdraws one with `$/cancel_request`. The SDK sets the
  `Responder::cancellation()` marker on an incoming `$/cancel_request`, and a
  dropped `SentRequest` sends one (`agent_client_protocol::concepts::cancellation`).

## Design

**Protocol (`ox-protocol`).**

- `SessionState::AwaitingInput` (`awaiting_input`).
- `EventKind::ElicitationRequested { request_id: String, message: String,
  tool_call_id: Option<ToolCallId>, requested_schema: ElicitationSchema }` and
  `EventKind::ElicitationResolved { request_id: String, action: ElicitationAction }`.
- `Method::ElicitationRespond { session_id, request_id, #[serde(flatten)] action:
  ElicitationAction }`, wire name `elicitation.respond`, so params read
  `{"session_id", "request_id", "action": "accept", "content": {...}}`. Add it
  to the `request_round_trip` test, since flattening an internally tagged enum
  must survive the round trip.
- `Client::respond_elicitation(session_id, request_id, action)`.

**State (`oxd/src/state.rs`).** `Pending` gains a kind:
`Permission { options, answer }` or `Elicitation { schema, answer:
oneshot::Sender<ElicitationAction> }`. Elicitations share `next_request` with
permissions, so request IDs stay unique per session and `fill` counts both.

- `Change::ElicitationRequested { message, tool_call_id, schema, answer }`
  records `ElicitationRequested`.
- `Change::ElicitationWithdrawn`: drops every pending elicitation whose
  `answer.is_closed()` and records `ElicitationResolved { action: Cancel }` for
  each. The handler drops its receiver before recording it, so no request ID
  has to cross back to the handler.
- `Live::state`: a pending permission gives `AwaitingPermission`, else a
  pending elicitation gives `AwaitingInput`, else as today. `quiet` counts both.
- `Change::Cancelled` cancels permissions only. `Live::end` and `fill` cancel
  both kinds (`ElicitationAction::Cancel`).
- `Request::Respond { request_id, action }`: not pending →
  `not_pending` (generalize its message to "request {id} is not pending");
  `Accept` content is checked by `check_content(schema, content)`; `Decline`
  and `Cancel` pass; `Other` → `INVALID_REQUEST`. Answering a permission ID
  with `Respond`, or an elicitation ID with `Permit`, is `not_pending`.
- `check_content`: every key names a property; values match the property:
  string (`enum`/`oneOf` membership, `minLength`/`maxLength` in chars), number
  (`Integer` or `Number`, `minimum`/`maximum`), integer, boolean, array of
  strings (membership in `items` enum or anyOf, `minItems`/`maxItems`); a
  property of unknown type takes any value; every `required` name is present.
  `pattern` and `format` are not checked here.
- `check_form(request) -> Result<(message, tool_call_id, schema), Error>`:
  refuses with `-32602` a non-form mode, a request scope, a required property
  of unknown type, a required name with no property, and a property with both
  `enum` and `oneOf` (the spec's "cannot render" cases).

**Session (`oxd/src/session.rs`).** `InitializeRequest` carries
`ClientCapabilities::new().elicitation(ElicitationCapabilities::new().form(ElicitationFormCapabilities::new()))`.
A `CreateElicitationRequest` handler runs `check_form` (error → respond with
it), records `ElicitationRequested`, then spawns a task that selects between the
answer receiver and `responder.cancellation().cancelled()`. Answer → respond
with it; sender dropped → respond `Cancel`; cancelled → drop the receiver,
record `ElicitationWithdrawn`, respond `Cancel`. `Session::respond_elicitation`
mirrors `permit`; `daemon.rs` dispatches `ElicitationRespond` to it.

**oxctl.** `describe` prints `elicitation <id>: <message>; fields: <name>
(<title>: <kind>)...`, where kind is `one of a, b`, `any of a, b`, `text`,
`number`, `integer`, `boolean`; resolved as `elicitation <id>: accepted
<content JSON>`, `declined`, or `cancelled`. `oxctl answer <session>
<request_id> [<content JSON>] [--decline | --cancel]`: content is required
unless a flag is given; it is parsed into the content map and sent as `Accept`.

**Fake agent.** `elicit`: sends `requires_action`, then an `elicitation/create`
whose `message` is `Order` and whose properties are `color` (string, title
`Color`, description `Pick a color`, `oneOf` `red`/`Red`/`Warm` and
`blue`/`Blue`/`Cool`, required), `extras` (array, title `Extras`, anyOf
`sprinkles`/`Sprinkles` and `nuts`/`Nuts`), `note` (string, title `Note`),
`count` (integer, title `Count`, 1 to 9), and `rush` (boolean, title `Rush`).
It selects between the response and `state.until_cancelled()`. Cancelled →
drop the request (the SDK sends `$/cancel_request`), say `withdrawn`, end
`cancelled`. Otherwise send `running` and say `accepted <content as compact
JSON>`, `declined`, or `cancelled`. Document it in the module comment.

**Docs.** `docs/acp-v2.md`: `initialize` (oxd sends `elicitation.form`),
`elicitation/create` and `elicitation.form` (oxd supported, caveats as above),
`$/cancel_request` (oxd: withdraws an open elicitation), `session/cancel` (oxd
leaves elicitations to the agent). `docs/architecture.md`: the lines about
pending permission requests cover elicitations too.

## Check your assumptions

1. **The request names ACP v1 types in schema 1.9.1; the plan uses the v2
   types.** Ox speaks v2 (`unstable_protocol_v2`) and Cargo.lock resolves schema
   1.17.0, so v1 types would not fit any existing handler. The wire format is
   the same.
2. **Sessions get a new `awaiting_input` state.** It is separate from
   `awaiting_permission`, and a pending permission takes precedence when both
   are open. Merging the two into one state is the alternative given up. A
   separate state lets the session list tell the two apart.
3. **Stop no longer resolves an open elicitation in oxd itself.** The
   elicitation ends when the agent withdraws it, exits, or the session parks or
   closes, as ACP v2 specifies. Permissions keep today's behaviour, where Stop
   resolves them. An agent that ignores the spec would leave a session awaiting
   input after Stop, so oxd could cancel elicitations on Stop as a fallback.
   The plan gives that up to follow the spec.
4. **oxd does not check `pattern` or `format`.** `oxctl answer` can send a value
   that breaks them. The app checks both through the browser's own input
   validation, and the spec says agents validate answers again. Checking them
   in oxd needs an ECMA-262 regex engine, which would be a new dependency.
5. **Some forms are refused outright.** oxd answers `-32602` to forms it cannot
   render: URL mode, request scope, a required property of unknown type, or a
   property with both `enum` and `oneOf`. Clients never see these.
6. **Permissions are unchanged.** An agent that withdraws a permission with
   `$/cancel_request` still leaves it pending in oxd. The withdrawal mechanism
   is built only for elicitations.

## Implementation sequence

1. `ox-protocol`: state, events, method, client call; round-trip tests.
2. `oxd/src/state.rs`: `Pending` kinds, changes, `Request::Respond`,
   `check_content`, `check_form`, `fill`; unit tests.
3. `oxd/src/session.rs` and `daemon.rs`: capability, handler, respond.
4. Fake agent `elicit`; `oxctl` describe and `answer`.
5. Daemon tests; docs.

## Verification

- `state.rs` unit tests: elicitation gives `AwaitingInput`, a permission
  alongside gives `AwaitingPermission`; `Cancelled` leaves an elicitation
  pending; `end` and `fill` resolve it `Cancel` with IDs continuing past it;
  `ElicitationWithdrawn` resolves only closed ones; `check_content` accepts a
  valid answer and refuses an unknown key, an unknown choice, a wrong type, a
  missing required property, too many items; `check_form` refuses URL mode and a
  required unknown-type property.
- `crates/oxd/tests/daemon.rs`:
  - `elicitation_round_trip`: prompt `elicit` and wait for
    `ElicitationRequested`. The state is `AwaitingInput`. An answer without
    `color` is refused `INVALID_REQUEST` and the state stays the same. Accept
    `{"color":"blue","extras":["nuts"]}`, then `ElicitationResolved` arrives
    with that content, followed by the chunk
    `accepted {"color":"blue","extras":["nuts"]}`. A second answer is refused
    `INVALID_STATE`.
  - `decline_reaches_the_agent`: the chunk is `declined`.
  - `cancel_withdraws_an_elicitation`: wait for `ElicitationRequested` and
    cancel. `ElicitationResolved` arrives with `cancel`, then the chunk
    `withdrawn`, then `TurnEnded` with `cancelled`. Without the `cancelled()`
    branch the resolution never arrives, so the test times out.
  - Extend `a_history_that_a_crash_left_open_ends_its_turn` with an open
    `ElicitationRequested`, which comes back resolved `cancel`.
- `oxctl`: a unit test of `describe` for both events.
- Commands: `make check-rust`, `make smoke-claude`. oxd changes, so the smoke
  check runs although the bridge does not ask yet.
