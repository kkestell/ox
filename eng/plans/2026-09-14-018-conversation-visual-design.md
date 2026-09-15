# Conversation visual design

## Goal

Make the selected conversation a readable scrolling transcript with its composer
fixed to the column's bottom. It follows live additions only while the reader is
at the latest item; scrolling away preserves that position and offers an
explicit return to the latest activity. Settle compact tool activity and plans
in the transcript, with blocking permission choices and form questions in a
stacked tray directly above the composer.

`eng/mockups/conversation.html` is the settled desktop and phone reference.

## Related code

- `client/src/components/app.tsx` — Owns the conversation's three bands and
  supplies the scroll element around `Transcript`.
- `client/src/components/transcript.tsx` — Renders transcript entries, current
  plans, and tool disclosures.
- `client/src/components/pending-interactions.tsx` — Supplies the permission and
  elicitation articles collected into the blocking tray.
- `client/src/components/composer.tsx` and `session-information.tsx` — Own the
  fixed prompt block, configuration controls, attachments, and usage line.
- `client/src/components/disclosure.tsx` — Keeps technical details beneath the
  activity they explain.
- `client/public/styles.css` — Already owns the conversation cascade layer and
  shell's fixed header/scroll/footer geometry.
- `client/e2e/smoke.spec.ts` — Has real-host fixtures for held prompts, tools,
  permissions, questions, replay, and phone navigation.
- `~/src/references/repos/personal/delta/web/src/components/ConversationPane.svelte`
  — Established prior art for bottom-distance pinning and observing streamed DOM
  changes rather than assuming each update changes a React prop.

## Decisions

The existing middle conversation band remains the only transcript scrollport;
the header and composer do not move. Transcript content has one centered reading
width, while a user's compact prompt aligns to its trailing edge and agent text
stays unboxed. Tool calls are compact, unboxed disclosures: a chevron, the
actual tool name, and one small status mark share a single line. The mark's
meaning remains available to assistive technology and on hover, but repeated
words such as “completed” and “in progress” do not consume transcript space.
Opened technical output flows beneath that line with a light left rule. Current
plans alone earn a quiet bordered transcript surface.

Follow state is local, ephemeral browser presentation. A newly shown
conversation starts at its bottom. The scroll handler considers the view
following when its bottom distance is within a small tolerance. While following,
new rendered transcript height and scrollport resizes return it to the bottom;
once the reader scrolls away, no live update changes `scrollTop`. A visible
`Jump to latest` button is the only action that re-enables following. This does
not enter the host snapshot or change replay, session, or prompt semantics.

Plans remain the controller's current projection rather than durable messages:
they appear once, after the transcript entries, as a compact progress checklist
whose completed items are marked without hiding their text. A tool's actual name
(for example, `read_file` or `shell`) is its summary; arguments, paths, output,
and any ACP title stay in its disclosure rather than being rewritten as an
invented activity description.

Pending permissions and elicitation forms leave the scrolling transcript. The
conversation renders one labelled tray, in normal column flow between the
scrollport and composer, that stacks every unresolved interaction. Its location
keeps a blocking request in view at the point where the user would otherwise
compose, while its normal flow means the transcript never sits behind it. Each
card clips its header background to the outer radius and keeps the existing
accessible article name, button wording, form controls, and callbacks. Moving
the presentation does not change the host-owned interaction identity or its
association with the tool that requested it.

## Test plan

- Extend the Playwright fake-provider fixture with a held reply against a
  transcript tall enough to scroll. Prove a newly opened conversation and a
  reader already at the bottom follow the released update.
- Scroll that same real transcript away from the bottom before releasing a
  streamed update. Assert its scroll position is preserved, `Jump to latest`
  appears, and activating it reaches the newest item and restores follow mode.
- Keep the existing real-host permission and elicitation scenarios; extend them
  to assert the labelled pending-interactions tray is immediately above the
  composer, stacks multiple requests, and retains each existing article,
  choices, and form controls.
- Add focused geometry assertions that the transcript scrollport has overflow
  while the composer remains at the conversation's bottom. The next TODO task
  owns the complete desktop/phone accessibility and visual-flow matrix.

## Implementation plan

- Refactor the conversation portion of `app.tsx` so a dedicated transcript
  scroll component owns the scrollport ref, renders `Transcript` inside it, and
  places the return-to-latest control over the scrollport without adding host
  state or a second transcript copy. Render a labelled `Pending interactions`
  tray after that scrollport and before the existing composer footer.
- Implement the local follow controller with a bottom-distance threshold,
  initial selected-session positioning, and a `ResizeObserver` or equivalent
  rendered-content observer. It must observe streamed text, tool updates,
  expanded disclosures, images, and the composer changing the scrollport size;
  it scrolls only while following and disconnects on session changes/unmount.
- Give `transcript.tsx` stable presentation class/data hooks for user, agent,
  thought, tool status, and plan placement. Render every tool as a compact
  disclosure labelled by its actual name, with a visually compact but accessible
  status mark and full-width opened output. Preserve ordered entries, safe
  resource rendering, and technical detail without rendering interactions in the
  scrollport.
- Add a pending-interactions tray in `pending-interactions.tsx` and minimal
  class hooks there, in `composer.tsx`, and in `disclosure.tsx` for the stacked
  cards, action rows, and tool disclosure. Do not rename an accessible control
  or alter the callback it invokes.
- Extend the `conversation` layer in `client/public/styles.css` from
  `eng/mockups/conversation.html`: reading-width transcript rhythm, compact user
  messages, disclosure-style tool rows, icon-sized accessible state marks, plan
  card, stacked interaction tray, clipped card corners, sticky return control,
  composer surface, and small-width wrapping. Keep the shell's existing
  responsive drawer and three-band layout intact.
- Add the focused real-browser scrolling and interaction assertions to
  `client/e2e/smoke.spec.ts`, including the fixture stream needed to make their
  scroll positions deterministic.

## Documentation updates

- `docs/spec.md` — Define that live transcript following yields to manual
  scrollback and that a reader can return to the latest activity.
- `eng/todo.md` — Check this conversation styling task off when the scoped
  implementation and tests land.
