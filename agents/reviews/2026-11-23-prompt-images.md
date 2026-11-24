# Review: attach images to a prompt

## Scope and coverage

Uncommitted changes on `prompt-images` against base `6f6443f`, the merge base
with `origin/main`, checked against
[`agents/plans/2026-11-23-prompt-images.md`](../plans/2026-11-23-prompt-images.md)
for issue #10. The working tree holds the plan, the new
`cmd/ox/web/src/Attachments.tsx`, and edits to 30 tracked files. The user
approved the plan's requirement text and kept *Session labels* unchanged.
Composer and bubble styling were compared with Hypercube's
`internal/ui/web/agent/composer.tsx` and
`internal/ui/web/transcript/transcript-images.tsx`.

The requirements diff matches the plan's Requirement changes word for word.

Gaps:

- The relay's image route and the page's drag outline were judged from the
  source and the implementer's headless Chrome check, not by a new browser run.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                                           |
| -- | -------- | ------------ | -------- | ------ | --------------------------------------------------------------- |
| F1 | Low      | Construction | Fix      | Resolved | The blank-text label rule is decided twice, and the two disagree |
| F2 | Low      | Correctness  | Fix      | Resolved | A drop on an archived session's transcript attaches hidden images |
| F3 | Low      | Conventions  | Rejected | —      | Alt text differs from the plan                                  |
| F4 | Low      | Intent       | Rejected | —      | A batch with any non-image file is refused whole                |
| F5 | Low      | Conventions  | Rejected | —      | Drop handlers sit under `biome-ignore` comments                 |
| F6 | Low      | Simplicity   | Rejected | —      | The page reads files with `FileReader.readAsDataURL`            |

## F1. The blank-text label rule is decided twice, and the two disagree

Low · Construction · Fix · Resolved · `internal/server/session.go:436`, `internal/store/store.go:320`

Whether a prompt can label its session is decided live by Go's
`strings.TrimSpace`, and after a restart by SQL `trim` over space, tab, LF,
and CR. Go also trims Unicode spaces such as U+00A0 and U+3000; SQLite's
`trim` does not.

A session whose first prompt is an image with text `" "`, followed by
`hello`, is labeled `hello` while `oxd` runs. After a restart, `FirstPrompt`
returns the `" "` prompt, `firstLine` trims it to `""`, and the session
shows its short identifier for good, since no later prompt is considered.
*Session labels* then differs before and after *Surviving restarts*.

### Evidence

Traced from the source: `session.update` skips the prompt because
`strings.TrimSpace(" ") == ""`; `FirstPrompt`'s `trim(..., ' '||char(9,
10, 13))` keeps it; `server.go:69` takes `firstLine` of that one text.

### Action

Decide the rule once, in Go. Have the store hand the server the user prompts'
texts in order (a rows iterator, or a query the server scans), and have both
`labelFrom` and startup take the first text whose `firstLine` is nonblank.
`labelFrom` then returns early on a blank `firstLine`, and the
`strings.TrimSpace` condition in `update` and the SQL `trim` go away. Extend
`TestSessionLabel`: an image prompt with text `" "`, then `hello`, keeps
the label `hello` after the restart it already performs.

## F2. A drop on an archived session's transcript attaches hidden images

Low · Correctness · Fix · Resolved · `cmd/ox/web/src/SessionView.tsx:70`

An archived session shows the archived bar instead of the composer, but its
transcript `.log` keeps the drop handlers. Dropping an image there attaches it
to the control with no thumbnail and no error. After Unarchive, the composer
shows images the user saw nothing happen to, and the next send carries them.

### Evidence

Traced from the source: the `.log` handlers are unconditional, while the
composer that shows `Thumbnails` renders only inside `<Show when={!props.session.archived}>`.

### Action

Attach only while the composer is shown. On an archived session the
transcript still calls `preventDefault` on a file drag, so the browser does
not open the file, but attaches nothing. Pass whether the composer is shown
into `createImageDrop` (or bind the `.log` handlers to a variant that only
prevents the default). Add an e2e test in `session.spec.ts`: drop an image on
an archived session's transcript, unarchive, and see no thumbnail.

## F3. Alt text differs from the plan

Low · Conventions · Rejected · `cmd/ox/web/src/Attachments.tsx`, `cmd/ox/web/src/SessionView.tsx`

Thumbnails use the file's name and bubble images say "Attachment", where the
plan and Hypercube say "Attached image" and "Image". Biome's `noRedundantAlt`
refuses the latter, and the text is not visible. A file name tells a screen
reader which thumbnail a Remove Image button sits on, and the e2e specs locate
both by role and name. Kept as is.

## F4. A batch with any non-image file is refused whole

Low · Intent · Rejected · `cmd/ox/web/src/ox.tsx` (`attach`)

Hypercube attaches a batch's images and shows the error for the rest; this
change refuses the batch. *Attaching images* says only that other files are
refused with an error, which both satisfy. The picker's `accept` already
limits picks to images, so a mixed batch comes only from a drop or paste, and
the error tells the user what to drop again. Refusing the whole batch is the
simpler rule. Kept as is.

## F5. Drop handlers sit under `biome-ignore` comments

Low · Conventions · Rejected · `cmd/ox/web/src/SessionView.tsx`, `cmd/ox/web/src/NewSessionView.tsx`

`noStaticElementInteractions` flags `dragover`/`drop` on `.composer` and the
new-session `.log`. A drop target has no keyboard form; Attach Image and paste
cover the keyboard, and each comment says so. Giving the divs a role only to
quiet the rule would misdescribe them. Kept as is.

## F6. The page reads files with `FileReader.readAsDataURL`

Low · Simplicity · Rejected · `cmd/ox/web/src/ox.tsx` (`base64`)

It is the standard way to get base64 from a `File`, matches Hypercube's
`readImage`, and a read error rejects the send through `createAttempt` as the
plan's Settling decision requires. Kept as is.

## Checks run

- `make test`: passed (Go with the race detector, lint, web unit tests).
- `make e2e`, `make live`, and headless Chrome through `make dev`: taken from
  `/ox-work`'s final response, all passed; `make live` includes the new image
  prompt section.
- Tests proved: `/ox-work` reports each new or changed Go and e2e test failing
  on base `6f6443f`.
- Spot-checked removals in `internal/server/session.go`, each restored:
  - deleting a `prompt_cancelled` input's images: `TestPromptImages` fails
    ("a dropped prompt's image answered <nil>");
  - skipping blank-text prompts when labeling: `TestSessionLabel` fails ("an
    image-only prompt labeled").
- Styling compared with Hypercube: 64 px `object-fit: cover` thumbnails, a
  2.25 rem secondary remove button at the top right, a ghost muted paperclip,
  and bubble images in a wrapping row with a 0.5 rem gap, at most 16 rem tall,
  `object-fit: contain`, using lucide-static's `paperclip.svg` and `x.svg`.

## Verification 1

Checked the uncommitted tree on base `6f6443f` after `/ox-work fix`.

| ID | Status   | Evidence                                                                                                                                                                                                 |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `store.UserPrompts` replaces `FirstPrompt`; startup and `labelFrom` both skip a prompt whose `firstLine` is blank. With startup taking the first prompt regardless, `TestSessionLabel` fails ("after a restart, A is labeled \"SL5YCZXF\""); restored, it passes. |
| F2 | Resolved | `createImageDrop` takes `composing`, and `SessionView` passes `!archived`. With the `composing()` guard removed from `onDrop`, "an image dropped on an archived session's transcript is not attached" fails; restored, it passes. |

No new findings.

Checks: `make test` passed on the final tree. `make e2e` (153 passed) from the fix's report. `make live` and the `make dev` check from the first round stand; the fixes change neither how Ox drives Claude Code nor how the page reaches `oxd`.

## Verdict

Ready to commit.
