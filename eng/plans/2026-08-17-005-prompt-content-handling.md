# 2026-08-17-005. Prompt content handling

## Goal

Ox accepts a prompt made of text and resource links and refuses everything else.
Real clients let a person paste a screenshot, attach a recording, or @-mention a
file so its contents travel with the request. Today all three come back as
`-32602`.

Teach Ox the whole ACP content vocabulary: advertise the three prompt
capabilities, validate every variant at the boundary, and translate each into
the OpenRouter content part that carries it.

## Desired outcome

`initialize` advertises `image`, `audio`, and `embeddedContext`, so a client
stops hiding attachment and mention affordances.

A prompt mixing text, an image, an audio clip, a resource link, and an embedded
resource reaches the model as one user message whose parts follow the client's
order. Text stays text. An image becomes an `image_url` part holding a data URL.
Audio becomes an `input_audio` part holding the base64 payload and a format
taken from the MIME type. A resource link stays the escaped Markdown link it is
today. An embedded text resource becomes a text part naming its source and
fencing its contents. An embedded image or audio blob takes the same path as the
corresponding standalone block.

A block Ox cannot turn into model input — a PDF blob, say — fails the prompt
with `-32602` naming the MIME type. It fails before the turn is claimed, so the
session is left free for the next prompt.

A malformed block fails the same way, with a message that says which block and
what was wrong: an image with no data, data that is not base64, a resource
carrying neither text nor a blob.

## Summary of approach

Each of the three packages learns its own half of the vocabulary, and the
translation stays in the middle.

`internal/acp` grows `ContentBlock` into the full v1 content vocabulary — the
existing `text` and `resource_link` fields plus `mimeType`, `data`, and a
`resource` object — and `MarshalJSON` gains the matching cases so every variant
round-trips. `PromptRequest.Validate` checks shape only: is this a variant Ox
accepts, are the fields the schema marks required present, does base64 decode.
`PromptCapabilities` gains the three booleans and the initialize response sets
them.

`internal/openrouter` gains the two multimodal content parts. `ContentBlock`
stays one flat struct and grows a `MarshalJSON` that emits
`{"type":"text",...}`, `{"type":"image_url","image_url":{"url":...}}`, or
`{"type":"input_audio","input_audio":{"data":...,"format":...}}`, following the
pattern `acp.ContentBlock` already sets.

`internal/agent` owns the routing decision, because deciding which provider part
a block becomes is exactly the translation the agent exists to do.
`promptMessage` returns an error instead of panicking, and the prompt handler
converts before claiming the turn, so an unroutable block never occupies a
session.

The split between the two checks is the point. `acp` answers "is this a
well-formed ACP block?" and knows nothing about OpenRouter. `agent` answers "can
this become model input, and as what?" and is the only place that knows an
`image/*` blob is a picture. Neither duplicates the other, so neither can drift
out of step with the other.

Capabilities are advertised unconditionally. Ox can translate all three kinds;
whether the configured model accepts them is the provider's answer, which
arrives as the provider's own error message. Gating the advertisement on the
model's declared input modalities needs the model catalog and belongs to that
item.

## Related code

- `~/src/references/repos/third-party/coding-agents/opencode/packages/opencode/src/acp/content.ts:29-113`
  — `contentBlockToParts`, the most complete content conversion in the
  references. Worth copying: an image becomes `data:<mime>;base64,<data>`; an
  embedded text resource is rendered with its source above the text; an embedded
  blob is routed by its MIME type. Its tests in
  `packages/opencode/test/acp/content.test.ts` show that a client sends the
  selected line range in the URI fragment, as `file:///tmp/context.txt#L12-L14`.
- `~/src/references/repos/personal/beta/src/acp.rs:719-747` — `prompt_to_text`,
  which handles embedded text resources and refuses blobs. `:217` advertises
  `embeddedContext` and nothing else. Its structure is right; this plan extends
  the refusal set rather than inheriting it.
- `~/src/references/repos/personal/alpha/runtime/internal/agent/loop.go:473-503`
  — `promptMessage` and the Markdown escaping helpers, already in Ox. Alpha's
  version returns an error rather than panicking, which is the signature this
  plan restores.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/schema/v1/schema.json`
  — `$defs.ImageContent`, `AudioContent`, `EmbeddedResource`,
  `TextResourceContents`, `BlobResourceContents`, and `PromptCapabilities` give
  the exact required-field sets. `docs/protocol/v1/content.mdx` is the prose.
- `~/src/references/repos/third-party/providers/openrouter-go-sdk/models/components/contentpartimage.go`
  and `contentpartinputaudio.go` and `multimodalmedia.go` — the wire names:
  `image_url` wraps `{"url": string}`, `input_audio` wraps
  `{"data": string, "format": string}`.
- `~/src/references/repos/personal/beta/openrouter-docs/guides/overview/multimodal/image-understanding.mdx`
  and `audio.mdx` — the provider's own examples. Images take either a URL or a
  `data:` URL; audio must be base64 and carries a format string, commonly `wav`
  or `mp3`, with support varying by provider.

## Current state

- Relevant existing behavior: `acp.ContentBlock` covers `text` and
  `resource_link` and marshals each variant's required fields.
  `PromptRequest.Validate` rejects every other type. `agent.promptMessage`
  converts the two variants and panics on anything else, because validation has
  already run. `acp.PromptCapabilities` is an empty struct and the initialize
  response sends `{}`.
- Existing patterns to follow: wire types and their `Validate` methods live in
  `internal/acp`; handlers turn a validation error into `-32602` with
  `jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)`; a variant struct carries all
  its variants' fields and a `MarshalJSON` switch emits the right subset.
- Prerequisite: the streaming prompt turn lands first. This plan changes
  `promptMessage`, the prompt handler, and the mock model's request decoding,
  all of which that work introduces.
- Constraints from the current implementation: the e2e mock model decodes a
  content part as `{type, text}` only, so it cannot see an image or audio part
  yet. `openrouter.ContentBlock` is marshal-only — nothing decodes it — which is
  what makes a `MarshalJSON`-only approach sufficient.

## Structural considerations

- **Hierarchy:** unchanged. `internal/acp` speaks the client's vocabulary,
  `internal/openrouter` speaks the provider's, and `internal/agent` is the only
  package that has both in scope. Each side grows the vocabulary it owns.
- **Abstraction:** shape validation and routing are different questions at
  different levels, so they live in different packages. `acp` must not learn
  that an `image/png` blob is something a model can look at, and `openrouter`
  must not learn what an ACP content block is. That is why `promptMessage`
  returns an error: a block that is well-formed but unroutable is a real
  outcome, not a bug, and only the agent can detect it.
- **Modularization:** conversion stays in `internal/agent/prompt.go` beside the
  turn it feeds. No new package: this is more vocabulary for existing types, not
  a new responsibility.
- **Encapsulation:** the embedded resource's `text` and `blob` are pointers so
  validation can tell an empty text resource from a missing one and report the
  difference. Nothing else about the block types changes shape.
- **Testability:** every rule is observable at the process boundary. The mock
  model records the request body, so the exact part a block became is an
  assertion on real HTTP the binary sent. Conversion, validation, and the two
  rendering helpers are pure functions with table tests.

## Refactoring

- `promptMessage` returns `(openrouter.Message, error)` instead of panicking,
  and the prompt handler calls it after the session lookup and before `claim`.
  This is preparatory: the routing failures this plan introduces have nowhere to
  go otherwise, and converting before claiming keeps a rejected prompt from
  occupying a session it never used.

## Test plan

- **Key behaviors to verify:**
  - `initialize` advertises `image`, `audio`, and `embeddedContext` as true.
  - A prompt of text then an image produces a user message whose parts are, in
    order, `{"type":"text"}` and `{"type":"image_url"}` with
    `data:image/png;base64,<data>`. Order follows the client's, unchanged.
  - An audio block produces `{"type":"input_audio"}` carrying the base64 data
    and a format derived from the MIME type: `audio/wav` and `audio/x-wav` give
    `wav`, `audio/mpeg` and `audio/mp3` give `mp3`, `audio/flac` gives `flac`.
  - An embedded text resource produces one text part: a label line, then the
    text inside a fence. A `file:` URI renders as its filesystem path, and an
    `#L12-L14` fragment renders as `:12-14`. Any other URI renders verbatim.
  - Text containing a run of backticks gets a fence one backtick longer, so the
    block still closes where it should.
  - An embedded blob resource whose MIME type is `image/*` produces an
    `image_url` part, and one whose MIME type is `audio/*` produces an
    `input_audio` part.
  - An embedded blob with any other MIME type, and one with no MIME type at all,
    each fail with `-32602` naming the problem. The session accepts a normal
    prompt afterwards, proving no turn was claimed.
  - A resource link still renders as an escaped Markdown link.
  - A second prompt resends the first user message with its non-text parts
    intact, so multimodal history accumulates like text history.
  - Each malformed block fails with `-32602`: an image or audio block with no
    `data`, with no `mimeType`, or with `data` that is not base64; a resource
    with no `uri`, with neither `text` nor `blob`, with both, or with a `blob`
    that is not base64; and a block whose type Ox does not know.
- **Test levels:** the behaviors above run through the harness against the built
  binary and the mock model, asserting on the request the mock recorded. Unit
  tests cover `PromptRequest.Validate` over a table of every variant and every
  malformed case, `acp.ContentBlock` marshalling for all five variants,
  `openrouter.ContentBlock` marshalling for its three, the audio format
  derivation, the resource label rendering, and the fence sizing.
- **Edge cases and failure modes:** an empty text block, which must still send
  `{"type":"text","text":""}`; an embedded text resource whose text is empty,
  which must be accepted rather than read as a missing field; resource text that
  both starts and ends with backticks; a `file:` URI with a percent-encoded
  path; a `#L12` fragment with no range; a fragment that is not a line
  reference, which is ignored rather than rendered.
- **What not to test:** whether the configured model actually accepts an image,
  whether the base64 is a decodable picture, and how complete anyone's MIME type
  registry is.

## Implementation plan

1. Widen the mock model's request decoding in `internal/e2e/model_test.go`:
   `modelContentPart` gains optional `image_url` and `input_audio` objects.
   `text()` keeps concatenating the text fields. Without this the tests cannot
   see the parts they are about.
2. Extend `acp.ContentBlock` with `MIMEType`, `Data`, and
   `Resource *EmbeddedResource`, and add `EmbeddedResource` with `URI`,
   `MIMEType`, `Text *string`, and `Blob *string`. The two pointers are what let
   validation distinguish an empty payload from an absent one.
3. Extend `ContentBlock.MarshalJSON` with the `image`, `audio`, and `resource`
   cases, each emitting the fields its variant requires and omitting the
   optional ones it lacks.
4. Extend `PromptRequest.Validate`: `image` and `audio` require `data` that
   decodes as standard base64 and a `mimeType` containing a `/`; `resource`
   requires the object, a `uri`, and exactly one of `text` or `blob`, with a
   blob that decodes as standard base64. Every message names the offending
   block's position and what was wrong.
5. Give `acp.PromptCapabilities` the three booleans and set all three in
   `Agent.Initialize`.
6. Add the OpenRouter content parts: `ContentBlock` gains `ImageURL`,
   `AudioData`, and `AudioFormat`, plus a `MarshalJSON` switching on `Type` to
   emit `text`, `image_url`, or `input_audio` in the provider's nested shape.
7. Rewrite `promptMessage` to return an error and cover the five variants. Text
   and resource links keep their current rendering. An image becomes an image
   part holding `data:<mimeType>;base64,<data>`. Audio becomes an audio part
   with the data and the derived format. An embedded text resource becomes the
   labelled fenced text part. An embedded blob routes on its MIME type prefix:
   `image/` and `audio/` take the paths above, and anything else — including a
   blob with no MIME type — is an error naming what could not be sent.
8. Add the three helpers beside it: `audioFormat`, mapping a MIME type to the
   provider's format string with `mpeg` and the `x-` and `wave` spellings
   normalized and any other subtype passed through; `resourceLabel`, rendering a
   `file:` URI as its decoded path with an `L<start>` or `L<start>-L<end>`
   fragment appended as `:start` or `:start-end` and anything else verbatim; and
   `fence`, returning a run of backticks one longer than the longest run in the
   text, with three as the floor.
9. Move the `promptMessage` call in the prompt handler ahead of `claim` and
   report its error as `-32602`.

## Documentation updates

- Roadmap item completed: prompt content handling. No ACP method coverage
  changes — this adds no methods.

## Impact assessment

- Code paths affected: `internal/acp` types and validation, `internal/agent`
  prompt conversion and the initialize response, `internal/openrouter` content
  parts, and the e2e mock model's request decoding.
- Data, protocol, or schema impact: Ox advertises three capabilities it did not
  before, so clients will start sending content they previously withheld.
  Session history now holds non-text parts; it is still in memory only.
- Dependency or API impact: none. `encoding/base64`, `net/url`, and `strings`
  are all standard library.
- Deliberately deferred, and none of it half-built here: gating the advertised
  capabilities on the configured model's input modalities, which needs the model
  catalog; `annotations`, whose `audience` field can mark a block as meant for
  only one side of the conversation; PDF and other document blobs, which
  OpenRouter routes through a paid `plugins` parameter; fetching an image from a
  remote `uri` when a client omits the required `data`, which would have Ox make
  requests on a client's behalf; and an `UnmarshalJSON` for
  `openrouter.ContentBlock`, which the durable session log will need and nothing
  needs yet.

## Validation

- Tests to write and run: `go test -race ./...`.
- Static checks: `gofmt`, `go vet ./...`, `staticcheck ./...`, `dprint check`.
- Manual verification: with a real `OPENROUTER_API_KEY` and a vision-capable
  `OX_MODEL`, drive `initialize`, `session/new`, and a `session/prompt` carrying
  a small base64 PNG by hand, and confirm the model describes the picture.
  Repeat against a text-only model and confirm the failure reports the
  provider's own message rather than one Ox invented.
