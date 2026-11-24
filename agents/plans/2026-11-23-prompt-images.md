# Attach images to a prompt

## Goal

Resolves [#10](https://github.com/kkestell/ox/issues/10). The user attaches
images to a prompt from the composer by picking files, pasting, or dropping
them. Each attached image shows as a thumbnail with a remove button before
sending. After sending, the images show in the prompt's bubble in the
transcript, on every page and after a reload, and Claude receives them as
`image` content blocks. The composer follows Hypercube's
(`/Users/kyle/src/hypercube/internal/ui/web/agent/composer.tsx`): a Paperclip
ghost button left of the text, and 64 px thumbnails above it.

## Related code

- `internal/server/methods.go` — `sendSession` checks a prompt at the boundary,
  and `harness/list` returns the configuration. Images get checked here, and a
  new `session/image` method lives here.
- `internal/server/session.go` — `sendPrompt` builds the `agent.Input`.
  `update` records and broadcasts every update, so it sees each
  `prompt_cancelled`.
- `internal/agent/agent.go` — `Input`, `Update`, and `Harness`. `Input.Update`
  copies an input's fields into its lifecycle updates.
- `internal/claude/claude.go` — `writer.Prompt` writes the stream-json user
  message with string content. `internal/claude/prompt.go` `write` calls it.
- `internal/fake/fake.go` — `begin` echoes `Content` as the reply.
- `internal/ur/stream.go` — `message`, one conversation entry in OpenRouter's
  request shape, with string `Content`. `internal/ur/ur.go` — `request` saves
  each taken input as a `user` message; `save` and `load` write and read the
  conversation file; `body` and `conversation` build the request from it.
- `internal/store/store.go` `FirstPrompt` and `internal/server/session.go`
  `labelFrom` — the session label from the first prompt the user sent.
- `internal/store/store.go` — the schema, and `events` rows that cascade with
  their session.
- `internal/server/mcp.go` — `renderEntry` renders `read_session` lines.
- `cmd/ox/main.go` — the relay. `/rpc` caps bodies at 8 MiB, and `/events` is
  the only path from `oxd` to the page.
- `cmd/ox/web/src/ox.tsx` — `createSessionControl` owns composer text, the
  `edits` count that decides whether a send clears the box, and the `sent`
  prompt that returns to the composer when dropped. `reload` loads every listed
  session's `session/get` on each connection.
- `cmd/ox/web/src/SessionView.tsx`, `NewSessionView.tsx` — the composer forms
  and `PromptBubble`.
- `cmd/ox/web/src/protocol.ts` — protocol types. `cmd/ox/web/src/style.css` —
  lucide-static icon classes and composer styles.

## Decisions

- **Images go by reference, not inline.** Every connection receives every
  update, `reload` fetches every session's history on each connection, and
  `oxd` drops a connection whose write takes over five seconds. Inline base64
  would send each image to every page twice (`prompt_queued`, `prompt`) and
  again on every reconnect, and a 13 MiB line over Tailscale on a phone would
  break the connection. So `oxd` stores each image in a new `images` table, and
  updates and snapshots carry only its `image_id`. The page loads an image from
  `ox` at `GET /images/{session_id}/{image_id}`. The relay fetches it with
  `session/image` over a fresh socket connection of its own, so image bytes
  never travel the page's event stream.
- **Protocol shape.** `session/send` takes optional `images`, an array of
  base64 strings. `oxd` decodes them and takes each one's type from its bytes
  with `http.DetectContentType`. It accepts `image/png`, `image/jpeg`,
  `image/gif`, and `image/webp`, and refuses anything else with `-32602`. The
  bytes are the single source of the type: the page sends none, and the relay
  can serve only these four types, never HTML or SVG. The decoded total is at
  most 10 MiB (`10 << 20`), checked after decoding, like the text limit. A
  prompt needs nonblank `text`, images, or both; one with blank text and no
  images returns `-32602` `session_id and text or images are required`.
  `prompt_queued`, `prompt`, and `prompt_cancelled` updates, and
  `session/get`'s `queued` items, carry `images`, an array of image IDs, when
  the input has any. Messages from other sessions carry none. Every harness
  takes images, so `agent.Harness` and `harness/list` do not change.
- **Claude content.** With images, `writer.Prompt` writes `content` as one
  `image` block per image (`source` `{type: base64, media_type, data}`). A
  `text` block follows only when the text is nonblank, so an image-only prompt
  is image blocks alone. Images come first because Claude reads images best
  before the text that refers to them. Without images, it writes the string
  content it writes today.
- **Ur content.** `message.Content` in `internal/ur/stream.go` becomes a
  `content` value: `Text string` and `Images []string`, the images as data
  URLs (`data:<mime_type>;base64,<data>`). It marshals as a JSON string when it
  has no images, as today. With images, it marshals as OpenRouter's content
  parts: one `{"type":"image_url","image_url":{"url":<data URL>}}` per image,
  then `{"type":"text","text":...}` when the text is nonblank. It unmarshals
  from either shape. The field uses `omitzero`. `request` saves each taken
  input as `content{Text: input.Content, Images: <data URLs of input.Images>}`.
  So the conversation file keeps the images, every later request re-sends
  them, and a resumed conversation still has them. Existing conversation files,
  whose content is always a string, load unchanged. Assistant merges in
  `conversation` and the reply read in the turn's end use `Content.Text`. A
  model that cannot read images fails the turn with OpenRouter's error, as any
  refused request does. The conversation file grows by about 13.3 MiB for a
  prompt with 10 MiB of images.
- **Image-only prompts.** The session label comes from the first prompt the
  user sent with nonblank text. `labelFrom` skips a prompt whose text is blank,
  so `session/labeled` is still sent once, and `FirstPrompt` selects the first
  user prompt with nonblank text, so the label is the same after a restart. An
  image-only prompt's bubble shows only its images. `read_session` renders it as
  `User: [images: N]`.
- **Requirements.** *Attaching images* and *Attached images in the composer*
  (added), *Prompts as sent* and *Blank prompts* (changed). They hold through
  the request lifecycle as the text does, because the control owns both:
  - The control keeps attached images through view switches and reconnects.
  - A send in flight holds Send. A cut-off send shows its error, and the
    transcript shows whether Claude took the prompt (*Send cut off*).
  - A second page sees the pending bubble's images by URL.
  - A prompt dropped on Stop, exit, park, or a failed start returns its images
    with its text to the sending page's composer, ahead of images attached
    since.
  - *One transcript*, *Surviving restarts*, and *Deleting a session* hold
    because images are stored with the session and cascade with it.
  - *Ur sessions*: an Ur session resumes a conversation with images like any
    other, because the images are in its saved conversation.
- **Composer clearing.** Attaching or removing an image counts as an edit in
  the control's `edits` count. After a successful send, the composer clears its
  text and images if nothing changed since the submit, and keeps both
  otherwise. This is the rule *Composer after a send* already applies to text.
- **Drop target.** The transcript pane and the composer both take dropped
  image files, so a drop that misses the box does not navigate the browser to
  the file. While files are dragged over either, the composer box shows a
  dashed outline.
- **Settling.** Two new waits:
  - The page's send first reads each file to base64 with `FileReader`. A read
    error rejects the send, and the request's error shows through
    `createAttempt`.
  - The relay's image request waits on `oxd`'s answer. It ends when the answer
    arrives, `oxd` closes the connection, or the page's request ends, which
    closes the connection as `/events` does. `session/image` always answers
    through `onSession`.
- **Process I/O.** A prompt with 10 MiB of images is one line of about 14 MB on
  Claude's stdin, written on the session goroutine as prompts are today.
  Claude reads stdin throughout. The case where it stops reading is open bug
  #33, and this change adds no new path to it.
- **Construction.** Each stored image has a type from the four allowed ones,
  because the only way into the store is `sendSession`'s decode-and-sniff,
  which returns `[]agent.Image`. Code past it never checks types or sizes. An
  input's image IDs and data live in one value, `agent.Image{ID, MimeType,
  Data}`, which marshals as its ID. So the snapshot's `queued` and the
  lifecycle updates carry exactly the IDs of the data the harness writes.
- **Ownership.** Stored images and draft images have separate owners:
  - The `images` table rows (`id`, `session_id` cascading with its session,
    `mime_type`, `data`) belong to the session goroutine. *Images in the
    transcript on every page and after a reload* needs them. `sendPrompt`
    writes them before `Send`; a failed write refuses the send with `-32000`,
    and nothing is sent. A failed `Send`, or a `prompt_cancelled` update with
    images seen in `session.update`, deletes them. A page fetching an image of
    a prompt just dropped gets a 404, and the drop has already removed the
    prompt's bubble. If `oxd` crashes while an image prompt is queued, its rows
    stay until the session is deleted.
  - Draft images (`File` objects) belong to the page's session control,
    beside its text. `sent` keeps the sent files until Claude takes or drops
    the prompt. Thumbnail object URLs are created and revoked by the thumbnail
    that shows them.
- **Claude Code.** *User messages* in `docs/harnesses/claude-code.md` records
  the image block. A Haiku probe on 2.1.331 settled the rest:
  - An image block with a text block, or with no text block, is taken like a
    text prompt. `command_lifecycle` reports it the same way, and the replay
    echo carries the image blocks.
  - A 6.75 MB PNG, a 9000×100 PNG, and JPEG data labeled `image/png` all
    succeed, and later turns and `--resume` still work.

  Claude normalizes the images itself, so Ox checks only type and total size.
  The probe's findings go into that section.
- **Trust boundaries.** `GET /images/...` sits behind `checkHost`, like
  `/events`. A cross-origin page can name the URL in an `<img>` but cannot read
  its pixels, and image IDs are `rand.Text()` values it cannot guess. Responses
  carry the sniffed type, `X-Content-Type-Options: nosniff`, and
  `Cache-Control: private, max-age=31536000, immutable`, since an ID never
  names other bytes. `/rpc`'s cap rises from 8 MiB to 24 MiB. That covers
  10 MiB of images as base64 (about 13.3 MiB), a 1 MiB prompt escaped to 6 MiB,
  and the envelope.

## Check your assumptions

1. **Ur sends images to its model.** Changed by the user from "Ur sessions take
   no images."
   - **Plan:** Ur sessions attach images like Claude sessions, and Ur sends
     them to its model. A model that cannot read images fails the turn with
     OpenRouter's error, and so does every later turn of that conversation,
     since the image stays in it.
2. **Image-only prompts are sent.** Changed by the user from "A prompt needs
   text."
   - **Plan:** With images attached and blank text, Send works. The session
     label waits for the first prompt with text.
3. **New requirement *Attaching images*.** Kept, minus its last sentence, as
   item 1 forces: "The user attaches PNG, JPEG, GIF, or WebP images to a prompt
   by picking files, pasting, or dropping them on the session, and sees each as
   a thumbnail that can be removed before sending. Other files, and images over
   10 MiB in total for one prompt, are refused with an error."
4. **New requirement *Attached images in the composer*.** Kept: "Attached
   images survive switching views and reconnecting as unsent text does, clear
   with the text after a send, and return to the composer with a dropped
   prompt's text."
5. **Changed *Prompts as sent*.** Kept. Old: "Prompts show in the transcript
   as the plain text the user sent." New: "Prompts show in the transcript as
   the plain text the user sent, followed by the images attached to them."
6. **Changed *Blank prompts*.** Changed, as item 2 forces. Old: "A blank prompt
   is not sent." New: "A prompt with blank text and no images is not sent."
7. **Unchanged *Session labels*.** Reopened by item 2. Kept.
   - **Plan:** The requirement stays as it is: "Any other session is labeled
     with the first line of the first prompt the user sent it, or a short
     identifier before it has one." A session whose first prompt has only
     images keeps its short identifier until a prompt with text labels it,
     which a reader can take as the requirement's "before it has one."
   - **Alternative:** Reword it to "Any other session is labeled with the first
     line of the first prompt with text the user sent it, or a short identifier
     before it has one." That states the image-only case outright, and changes
     one more requirement.

## Test plan

- **Claude harness** (`internal/claude/prompt_test.go`, the test that checks
  the bytes `Send` writes): an input with two images writes `content` as two
  `image` blocks with each image's `media_type` and base64 data, then the
  `text` block. An input with an image and blank text writes the image block
  and no `text` block. The existing cases keep asserting string content without
  images.
- **Ur** (`internal/ur/ur_test.go`, new `TestImages`; no Ur test sends images
  today): against the stand-in OpenRouter server, an input with one PNG and
  text posts a `user` message whose `content` is an `image_url` part with the
  PNG's data URL, then the text part. An image-only input posts the `image_url`
  part alone. The next turn's request still carries the first prompt's parts. A
  new agent resumed from the same directory posts them too. A text-only prompt
  still posts string content, which `TestTwoTurns` already asserts.
- **Server** (`internal/server/methods_test.go`, new `TestPromptImages`; no
  test sends images today):
  - A fake session sent text with one PNG:
    - `prompt_queued` and `prompt` carry one image ID.
    - `session/image` returns `image/png` and the same base64.
    - `session/get`'s history prompt carries the ID.
    - The fake's reply reports one image.
  - An image-only prompt with blank text is accepted, and its `prompt` update
    carries the image ID.
  - A bad request returns `-32602` and starts nothing:
    - blank text with no images;
    - base64 that does not decode;
    - text bytes sent as an image;
    - 10 MiB plus one byte of PNG-signature data across two images.
  - A queued image prompt dropped by `session/interrupt` leaves its image
    answering `-32001`.
- **Server** (`TestSessionLabel` in `internal/server/methods_test.go`): an
  image-only first prompt leaves the short label and sends no
  `session/labeled`. The next prompt with text labels the session once, and a
  restarted server keeps that label.
- **Store** (`internal/store/store_test.go`): `DeleteSession` removes the
  session's images.
- **MCP** (`internal/server/mcp_test.go`, the `read_session` rendering test): a
  prompt with images renders its image count after its text, and an image-only
  prompt renders as `User: [images: 1]`.
- **Relay** (`cmd/ox/main_test.go`):
  - `TestRPCRefusesOversizedBody` moves to the 24 MiB cap.
  - `TestRPCRelaysOneMiBOfEscapedText` adds 10 MiB of images as base64 to its
    body.
  - A new `TestImageServesOxdImage` runs a socket stand-in that writes a
    notification and then the `session/image` answer. It checks that the relay
    serves the decoded bytes with the image's `Content-Type` and `nosniff`,
    answers 404 for `-32001`, and refuses a foreign `Host`. No relay test
    covers a GET route that dials `oxd` today.
- **e2e** (`cmd/ox/web/e2e/session.spec.ts`; images are 1×1 PNG buffers built
  in the spec):
  - New tests, one behavior each:
    - an image picked through Attach Image shows as a thumbnail;
    - a pasted image shows as a thumbnail;
    - a dropped image shows as a thumbnail;
    - Remove Image removes it;
    - a non-image file is refused with an error;
    - images over 10 MiB are refused with an error;
    - an image-only prompt sends and its bubble shows only the image;
    - a sent image shows in the prompt on the sending page, on another page,
      and after a reload, loaded (`naturalWidth` 1);
    - the composer clears its images after a send.
  - Existing tests that gain an image:
    - "Stop drops a pending prompt and returns its text to the composer" also
      returns its image;
    - the unsent-text test across view switches also keeps an attached image.
- **Live** (`TestLive` in `internal/server/live_test.go`): a Haiku prompt with
  a 64×64 PNG is taken with its image ID, and its turn's `result` is not an
  error.
- **Headless Chrome through `make dev`:** attach, send, and see the image load
  through the relay at the printed URL.

## Implementation plan

1. `internal/agent/agent.go`: add `Image{ID, MimeType string; Data []byte}`,
   whose `MarshalJSON` writes its ID. Add `Input.Images []Image`
   (`json:"images,omitempty"`), `Update.Images []string`
   (`json:"images,omitempty"`), and copy the IDs in `Input.Update`.
2. `internal/store/store.go`: add the `images` table (`id TEXT PRIMARY KEY`,
   `session_id ... REFERENCES sessions ON DELETE CASCADE`, `mime_type`,
   `data BLOB`), plus `AddImages` (one transaction), `Image`, and
   `DeleteImages`, with a `store.Image` type. `FirstPrompt` selects the first
   user prompt whose text is nonblank.
3. `internal/server/methods.go`:
   - `sendSession` decodes `images` into `[]agent.Image`, checks the four
     types and the 10 MiB total, and requires nonblank text or at least one
     image. Add the messages to the `-32602` cases.
   - Add `session/image` through `onSession`, answering `{mime_type, data}`, or
     `-32001` `image not found`.
4. `internal/server/session.go`:
   - `sendPrompt(text, images)` assigns image IDs with `rand.Text()`, stores
     them, sends, and deletes them if `Send` fails.
   - `update` deletes a `prompt_cancelled` input's images.
   - `labelFrom` skips a prompt whose text is blank.
5. `internal/claude/claude.go`, `prompt.go`: `writer.Prompt` takes the images
   and writes blocks as decided.
6. `internal/ur/stream.go`, `ur.go`: add `content` with its JSON shapes, make
   `message.Content` one, save each taken input's images as data URLs, and read
   `Content.Text` wherever text is joined or read.
7. `internal/fake/fake.go`: the reply appends ` [images: N]` when the input has
   images.
8. `internal/server/mcp.go`: `renderEntry` appends ` [images: N]` to a prompt
   with images, after its text when it has any.
9. `cmd/ox/main.go`:
   - Raise `/rpc`'s cap to 24 MiB and update its comment and message.
   - Add `GET /images/{session_id}/{image_id}` behind `checkHost`. It dials the
     socket, writes `session/image`, and skips lines without the request's
     `id`. It closes the connection when the request ends, and answers 404,
     502, or the decoded image with the decided headers.
10. `cmd/ox/web/src/protocol.ts`: `Prompt.images?: string[]`; `session/send`
    takes `images?: string[]`.
11. `cmd/ox/web/src/ox.tsx`, `createSessionControl`:
    - Add `images` (a `File[]` signal), `attach(files)`, and
      `removeImage(file)`. Both count as edits.
    - `attach` refuses types outside the four with "Attach PNG, JPEG, GIF, or
      WebP images." It refuses a total over 10 MiB by `File.size` with "Images
      can total at most 10 MiB." Both errors go through `attempt`.
    - `post` reads the files to base64 with `FileReader` and sends them. It
      clears text and images together when `edits` is unchanged.
    - `sent` keeps the files, and `finishPrompt` puts dropped ones ahead of
      the current images.
    - `create` posts the draft's images.
    - `send` and `create` take a prompt with nonblank text or at least one
      image.
12. `cmd/ox/web/src/SessionView.tsx`, `NewSessionView.tsx`:
    - The box becomes a column: a thumbnails row (when any), then an input row
      with Attach Image (`button.icon.paperclip`, ghost, `title`/`aria-label`
      "Attach Image"), the textarea, Stop, and Send.
    - Add a hidden `<input type="file" accept="image/png,image/jpeg,image/gif,image/webp" multiple>`.
    - The textarea's `onPaste` takes image files.
    - `.log` and `.composer` take `dragover` and `drop` of files and mark the
      box while dragging.
    - Each thumbnail is a 64 px `object-fit: cover` `<img alt="Attached image">`
      with a top-right `button.icon.x` "Remove Image". It uses an object URL
      revoked with `onCleanup`.
    - The textarea is `required` only while no image is attached, so the form
      submits an image-only prompt.
    - `PromptBubble` shows `images` below the text as
      `<img alt="Image" src="/images/{session_id}/{id}">` in a wrapping row with
      a gap, each at most 16 rem tall and the bubble's width, `object-fit:
      contain`. An image-only prompt shows no text line. The bubble needs the
      session ID passed in.
13. `cmd/ox/web/src/style.css`: add
    `.paperclip { --icon: url(lucide-static/icons/paperclip.svg); }`, the box
    column, thumbnail, remove-button, drag-outline, and bubble image rules.
14. Tests as in the test plan. Then docs and `make docs`.

## Requirement changes

- **Added** (under Sending prompts, after *Blank prompts*):
  - **Attaching images.** The user attaches PNG, JPEG, GIF, or WebP images to a
    prompt by picking files, pasting, or dropping them on the session, and sees
    each as a thumbnail that can be removed before sending. Other files, and
    images over 10 MiB in total for one prompt, are refused with an error.
  - **Attached images in the composer.** Attached images survive switching
    views and reconnecting as unsent text does, clear with the text after a
    send, and return to the composer with a dropped prompt's text.
- **Changed:** **Prompts as sent.**
  - Old: Prompts show in the transcript as the plain text the user sent.
  - New: Prompts show in the transcript as the plain text the user sent,
    followed by the images attached to them.
- **Changed:** **Blank prompts.**
  - Old: A blank prompt is not sent.
  - New: A prompt with blank text and no images is not sent.

## Documentation updates

- `docs/protocol.md`:
  - `session/send`'s `images`, their types and limit, and text or images
    being required;
  - `images` on `prompt_queued`, `prompt`, `prompt_cancelled`, and `queued`
    items;
  - the new `session/image` method;
  - `-32001` covering an image.
- `docs/architecture.md`, Web UI: the relay's `/images` route and the 24 MiB
  `/rpc` cap. The session control also owns the composer's attached images.
- `docs/harnesses/claude-code.md`, *User messages*: the 2.1.331 probe findings.
