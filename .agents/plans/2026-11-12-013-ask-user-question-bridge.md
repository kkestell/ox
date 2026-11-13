# The bridge asks Claude's AskUserQuestion questions as a form

- Task: `2026-11-12-013-ask-user-question-bridge`
- Kind: plan
- Status: ready
- Source HEAD: `82203839c1c7243c204a02f86aa0324dbe0bb515`
- Dependencies: `2026-11-12-012-elicitation-relay-in-oxd` (the smoke check
  answers through `oxctl answer`, and oxd must advertise form elicitation)

## Outcome and boundaries

When Claude calls AskUserQuestion and the client advertised form elicitation,
the bridge sends `elicitation/create` with one field per question, and the
answers reach Claude as `updatedInput` with `answers`, so the tool result
reports what the user picked. Decline or cancel denies the tool. When the
client lacks form elicitation, `claude` starts with AskUserQuestion disallowed,
and Claude asks in plain text. Other tool permissions are unchanged.

Non-goals: option previews (`toolConfig.askUserQuestion.previewFormat` stays
unset, so options carry no `preview`), the free-form `response` field, and
URL elicitation.

## Current system

- `crates/claude-code-acp/src/runner.rs` `Output::control_request`: every
  `can_use_tool` becomes a `PermissionRequest`. A spawned task calls
  `Updates::permission` and writes the `control_response`; its `AbortHandle`
  goes in `State.permissions`. `control_cancel_request` aborts it, as do
  `result` and exit. Aborting drops the in-flight `SentRequest`, and the SDK
  then sends `$/cancel_request`.
- `crates/claude-code-acp/src/updates.rs` `Updates::permission`: sends a
  pending `ToolCallUpdate` (marks it seen and open), holds `Asking`, which
  reports `requires_action`, then sends the request. Allow replies with
  `{"behavior":"allow","updatedInput":input,"toolUseID",...}`, deny with
  `{"behavior":"deny","message",...}`, and the tool call status follows.
- `crates/claude-code-acp/src/bridge.rs`: `Bridge::initialize` is an
  associated function that ignores client capabilities. `Bridge::prompt` builds
  `StartRequest` and `Updates::new`. `Claude::start` builds the `claude`
  command line.
- Claude Code docs (Agent SDK, "Handle approvals and user input"): the input is
  `questions[]` with `question`, `header` (≤12 chars), `options` (2–4 of
  `{label, description}`) and `multiSelect`, with 1–4 questions. Allow with
  `updatedInput: {questions, answers}`, where `answers` maps question text to
  the chosen label. Multi-select labels are joined with `", "`. An "Other"
  answer is the user's own text.
- Tests: `crates/claude-code-acp/tests/protocol.rs` drives the bridge binary
  against a shell `fake_claude` (`permission_request_round_trips_through_client`,
  `cancellation_stops_a_pending_permission`, helper `initialize`).

## Design

**Capability.** `Bridge` holds the client's form support (set once in
`initialize`, which becomes `&self`; `main.rs` calls it on the shared bridge).
`ElicitationCapabilities::supports_form` on `request.capabilities.elicitation`
decides it. `StartRequest` gains `ask_user_question: bool`. When it is false,
`Claude::start` adds `--disallowedTools AskUserQuestion`, keeping the variadic
list ahead of another `--` flag. `Updates::new` takes the same flag.

**Routing.** In `control_request`, a `can_use_tool` whose `tool_name` is
`AskUserQuestion` spawns `Updates::ask_user` instead of `Updates::permission`.
It is held in the same abort map, so withdrawal, turn end and exit cancel it
the same way. If the flag is false (Claude cannot call the tool), `ask_user`
denies at once without contacting the client, because agents must not request a
mode the client has not advertised.

**Mapping (new module `crates/claude-code-acp/src/questions.rs`, pure
functions).**

- `form(input) -> Option<(String, ElicitationSchema)>`: `None` when `questions`
  is not a non-empty array of objects with `question` and `options`. Question
  `i` (1-based) gives these properties:
  - `q{i}`: `multiSelect` false gives a `StringPropertySchema` with
    `one_of` = options as `EnumOption::new(label, label).description(description)`.
    `multiSelect` true gives `MultiSelectPropertySchema::titled(options)`.
    Title = `header`, description = `question`.
  - `q{i}-other`: a `StringPropertySchema` titled `Other`.
  - No property is required, because a question answered only through
    `Other` has no selection.
  - Property names sort in question order (`q1`, `q1-other`, `q2`), and
    clients render in name order because `properties` is a `BTreeMap`.
  - Message: `Claude has a question`, or `Claude has questions` when there
    are several.
- `answers(input, content) -> Value`: returns `{"questions": input.questions,
  "answers": {...}}`. For each question:
  - single-select: non-blank `q{i}-other` text, else the `q{i}` label.
  - multi-select: the selected labels in option order, then the non-blank
    `Other` text, joined with `", "`.
  - A question with neither is left out of `answers`.
  - Values of the wrong type, or labels not in the question's options, are
    ignored, since the agent validates again.

**`Updates::ask_user(permission) -> Value`.** It sends the same pending
`ToolCallUpdate` preamble as `permission` (factor it out; kind
`ToolKind::Other`) and holds `Asking`. It then sends
`CreateElicitationRequest::new(ElicitationFormMode::new(ElicitationSessionScope::new(session).tool_call_id(tool_use_id), schema), message)`.

- `Accept` replies `{"behavior":"allow","updatedInput":answers(..),"toolUseID",
  "decisionClassification":"user_temporary"}`, and the tool call goes
  `in_progress`.
- `Decline` denies with `The user declined to answer.`
- `Cancel`, `Other` or an error denies with `The user did not answer.`
- Every denial uses `decisionClassification: "user_reject"`, and the tool call
  goes `failed`.
- If `form` returns `None`, deny with `The questions could not be shown.`
  without contacting the client.

**Docs.** `crates/claude-code-acp/README.md` gains an "AskUserQuestion"
section next to "Tool permissions". In `docs/acp-v2.md`, `elicitation/create`
and `elicitation.form` read supported for claude-code-acp with the mapping, the
plain-text fallback is described, and `$/cancel_request` notes that the bridge
withdraws an elicitation that Claude withdrew or that the turn ended.

**Smoke.** `scripts/smoke-acp.py` gets `check_question`, which runs after
`check_permission`:

- It prompts: "Use the AskUserQuestion tool to ask me one question with header
  Color, question 'Which color?', and options Red and Blue. After I answer,
  reply with only the color I chose."
- It waits for `elicitation_requested` and checks `\tawaiting_input\t` in
  `oxctl ls`, then disconnects, reattaches and checks that the replayed event
  matches.
- It runs `oxctl answer <session> <id> '{"q1":"Blue"}'`, finishes the turn,
  and checks that the reply contains `Blue`.

## Check your assumptions

1. **Nothing in the form is required.** Each question has a choice field and an
   `Other` text field, and neither is required, because an answer typed into
   `Other` has no choice. Submitting with a question blank sends Claude no
   answer for it. The alternative is an `Other` entry inside the required
   choice list, which would show "Other" twice: once as a choice and once as
   the text box's label.
2. **Text typed in Other replaces the choice on a single-select question.** On
   a multi-select question it is added after the chosen labels, joined with
   `", "`. A form cannot make the two fields exclusive. The alternative was to
   send both for a single-select question.
3. **The panel's title is generic.** It reads "Claude has a question" or
   "Claude has questions". Each question's header becomes its field title and
   the question text its description. Putting the question text in the message
   would repeat it in the field.
4. **Declining or cancelling denies the tool.** Claude is told "The user
   declined to answer." or "The user did not answer.", and may go on or ask in
   text.
5. **A client without form elicitation loses the tool for the whole `claude`
   process.** The bridge passes `--disallowedTools AskUserQuestion` from the
   capability the client sent in `initialize`. Claude asks its questions in
   plain text instead.
6. **The new smoke check depends on Haiku calling AskUserQuestion when told
   to.** If the model answers in text instead, `make smoke-claude` fails
   without any fault in the code.

## Implementation sequence

1. `questions.rs` with `form` and `answers`, plus unit tests.
2. `bridge.rs` and `main.rs`: capture the capability; `StartRequest` and
   `Updates::new` carry it; `runner.rs` adds the flag and routes the tool.
3. `updates.rs`: shared preamble, `ask_user`.
4. Protocol tests, docs, smoke check.

## Verification

- `questions.rs` unit tests:
  - `form` builds the property names, titles, descriptions, oneOf/anyOf
    options with descriptions, and message for a single-select and a
    multi-select question, and returns `None` for malformed input.
  - `answers` covers a label, Other overriding a label, multi-select labels
    plus Other joined in option order, a blank question omitted, and an
    unknown label ignored.
- `tests/protocol.rs`:
  - `ask_user_question_becomes_a_form`: the fake claude emits `can_use_tool`
    for `AskUserQuestion` with two questions. The client, which advertises
    `elicitation.form`, receives `elicitation/create` with `toolCallId` and the
    expected schema, and the session is `requires_action`. It accepts
    `{"q1":"Summary","q2":["Introduction"],"q2-other":"Appendix"}`. The fake
    claude exits non-zero unless the response contains `"behavior":"allow"`
    and `"answers":{"How should I format the output?":"Summary","Which sections should I include?":"Introduction, Appendix"}`.
    The turn then ends `end_turn`.
  - `declined_questions_deny_the_tool`: the response contains
    `"behavior":"deny"`.
  - `withdrawn_questions_cancel_the_elicitation`: the fake claude sends
    `control_cancel_request` after an interrupt, and the client receives
    `$/cancel_request` naming the elicitation's request ID.
  - `clients_without_forms_disallow_ask_user_question`: with capabilities
    `{}`, the fake claude exits non-zero unless its arguments include
    `--disallowedTools AskUserQuestion`. With `elicitation.form`, they must
    not. Removing the flag makes the first case fail.
- Commands: `make check-rust`, `make check-scripts`, `make smoke-claude`.
