# Functional requirements

What the person using Ox can see and rely on. Each requirement is cited by its
title.

## Workspaces

- **Adding a workspace.** The user adds a workspace by giving the absolute path
  of an existing directory; adding the same directory again changes nothing. The
  session list shows it by its directory's name.
- **Removing a workspace.** A workspace can be removed only when it has no
  sessions, archived ones included; otherwise its menu shows Remove workspace
  unavailable, with the reason. Removing it asks first, and leaves the directory
  on disk.

## Session list

- **Session order.** Each workspace lists its top-level sessions newest first,
  each followed by its child sessions, oldest first and indented beneath it; a
  child's own children follow it the same way, indented one level further.
- **Session labels.** A child session is labeled with the title it was created
  with. Any other session is labeled with a short title the configured titling
  model writes from the first prompt the user sent it, which replaces the label
  everywhere it shows once it arrives and lasts through restarts. Until then, or
  if titling fails, the session is labeled with that prompt's first line,
  shortened, or a short identifier before it has one.
- **Session status.** Each session shows whether it is parked, idle, idle while
  background work Claude started still runs, running, waiting on the user, or
  archived, as soon as the session list shows it.
- **Collapsing child sessions.** A session with child sessions has a control on
  its row that hides or shows every session beneath it; clicking elsewhere on
  the row opens the session. Children start shown, a child created under a
  collapsed session stays hidden, hiding the open session leaves it open, and
  reloading the page shows every child again.
- **Session URL.** The URL names the selected session, so reloading the page
  reopens it.
- **Menus.** A session's and a workspace's actions open from its menu button or
  by right-clicking it, and an unavailable action shows why.

## Creating sessions

- **New-session defaults.** A new session starts with the configured default
  harness, model, effort, and permission mode.
- **New-session choices.** Before sending, the user can pick any offered
  harness, model, effort, and permission mode for a new session. The user finds
  a model by typing part of its name or its harness's.
- **Creating a session.** Submitting a new session creates it and sends the
  prompt exactly once.
- **Creation keeps the user's view.** A session whose creation finishes after
  the user has moved to another view does not take them away from it.
- **Failed creation.** If creating a session fails, its text and images return
  to the draft with its settings and the error, with a way back to the draft
  from any other view, and the user can submit again.
- **Lost creation reply.** If the page loses the reply to a creation but the
  session exists, the page opens the session with the prompt unsent and asks the
  user to review its settings and send.

## Sending prompts

- **Only the user sends.** A prompt is sent only when the user submits it, and a
  message only when another session sends it or Ox reports a child's turn to its
  parent: showing a view, reconnecting, or reloading sends nothing, and a failed
  send is never retried automatically.
- **Send cut off.** A send cut off by a lost connection shows an error even if
  Claude received the prompt; the transcript shows whether it did.
- **Unsent text.** Unsent text, in a session or in a workspace's new-session
  draft, survives switching views and reconnecting. Reloading the page discards
  it, and removing a workspace discards its draft.
- **Composer after a send.** Sending clears the composer at once. If the send
  fails, its text and images return to the composer ahead of anything typed
  since.
- **Blank prompts.** A prompt with blank text and no images is not sent.
- **Attaching images.** The user attaches PNG, JPEG, GIF, or WebP images to a
  prompt by picking files, pasting, or dropping them on the session, and sees
  each as a thumbnail that can be removed before sending. Other files, and
  images over 10 MiB in total for one prompt, are refused with an error.
- **Attached images in the composer.** Attached images survive switching views
  and reconnecting as unsent text does, clear with the text after a send, and
  return to the composer with a dropped prompt's text.
- **Enter to send.** On a computer, Enter sends and Shift+Enter starts a new
  line; Enter while composing with an input method does not send. On a phone or
  tablet, Return starts a new line and only Send sends.
- **Prompt size limit.** A prompt over 1 MiB is refused with an error and stays
  in the composer.
- **One prompt at a time.** While Claude works, the user can send a prompt,
  which Claude takes into the running turn after its current step, or runs next
  if the turn ends first. Until Claude takes it, the prompt shows as pending at
  the end of the transcript and Send is unavailable, as it is while a message
  from another session is pending or waiting; once taken, it becomes a normal
  prompt where Claude took it.
- **Stop.** While Claude works, a Stop button shows. Stop ends the turn, the
  transcript shows it was interrupted, and the session takes prompts again; a
  pending prompt is dropped, and its text returns to the composer of the page
  that sent it, ahead of anything typed since. Messages from other sessions that
  Claude has not taken are dropped, and their senders are not told.

## Transcript

- **Session header.** A session's view shows its label and the directory it
  works in.
- **Prompts as sent.** Prompts show in the transcript as the plain text the user
  sent, followed by the images attached to them.
- **Messages in the transcript.** A message shows in the recipient's transcript
  rendered as Markdown under the same rules as replies, headed with the label of
  the session that sent it, pending at the end of the transcript until Claude
  takes it, on every page and after a reload.
- **Replies as Markdown.** Claude's replies stream in as Markdown. Raw HTML
  shows as text, images are not loaded, unsafe links are not links, and links
  open in a new tab.
- **Tool calls.** Each tool call shows its name, a summary of its input, and
  whether it is running, done, failed, or stopped; expanding it shows the full
  input and the result's text, and a plan renders as Markdown. Expanding or
  collapsing a tool call keeps its summary where it was on screen.
- **Reasoning.** When Claude or Ur reasons, the transcript shows the reasoning
  in full where it happened, streaming in as Markdown under the same rules as
  replies. Claude's reasoning is its own summary of its thinking.
- **Main conversation only.** The transcript shows the main conversation only,
  not the inner messages of Claude's subagents.
- **Failed turns.** A failed turn shows its error in the transcript.
- **Unexpected exits.** A Claude process that exits without Ox stopping it
  leaves the reason in the transcript.
- **Following output.** The transcript follows new output while it is scrolled
  to its bottom.
- **Claude's own turns.** Turns Claude starts itself, from a schedule or a
  finished background task, run and appear in the transcript like any other
  turn.
- **One transcript.** The transcript is the same on every page and after a
  reload.

## Settings

- **Settings shown are settings used.** The settings shown for a session are the
  ones Claude runs with.
- **Setting commands.** A prompt whose first word is `/model` or `/effort` is
  refused with an error and stays in the composer; the model and effort change
  only through the session's settings. Other slash commands are sent as typed.
- **Pending setting changes.** A settings change reaches Claude before the next
  prompt. While it is pending, the page marks the changed setting as pending and
  holds Send. If it fails, the page shows the error and the settings Claude
  actually has.
- **Setting changes keep the rest.** Changing a setting keeps the conversation
  and leaves the other settings alone.
- **When settings can change.** Settings can change only while the session is
  idle or parked, and not while it is archived.
- **Model within its harness.** An existing session can change its model only
  within its harness.
- **Effort across models.** Choosing a model keeps the effort when the new model
  offers it.
- **Retired settings.** A model or permission mode the configuration no longer
  offers still shows as the session's setting.
- **Claude's own mode changes.** When Claude changes its own permission mode, as
  when leaving plan mode, the page shows the new mode and later processes use
  it.

## Approvals

- **Permission requests.** A tool permission request appears in its session on
  every page, including after a reload, until it is answered or withdrawn.
- **Allow and Deny.** Allow approves the tool's input exactly as shown; Deny, or
  a typed reply, denies with that message.
- **Questions from Claude.** A question from Claude appears as a form with its
  options and an Other answer, one question at a time. The user moves between
  questions with Previous and Next without losing answers, moves on only once
  the shown question is answered, submits from the last question, and can
  decline from any question.
- **Approvals wait.** An approval waits for an answer, and the session does not
  park meanwhile. It is withdrawn on every page when Claude cancels it, goes
  idle, or exits.
- **One answer per approval.** An approval already answered, from this page or
  another, refuses a second answer with an error.

## Sessions working together

- **Claude can list Ox's models.** Claude can look up the harnesses, models,
  effort levels, and permission modes Ox offers, in any permission mode, without
  asking for approval.
- **Claude can message other sessions.** Claude can list the other sessions that
  are not archived, with each one's label, workspace, settings, and status, and
  send any of them a message. It does so without asking for approval, except in
  plan mode, where each message asks first.
- **Claude can read other sessions.** Claude can read any other session's
  transcript and current status, in any permission mode, without asking for
  approval.
- **Claude can start child sessions.** A session's Claude can start a child
  session with a title and a first message, which reaches the child as a message
  from its parent, without asking for approval except in plan mode, where Claude
  asks first. The child takes its parent's permission mode, and its parent's
  harness, model, effort, and directory unless Claude picks others Ox offers,
  and can start children of its own up to the configured depth.
- **Message delivery.** A message from another session reaches Claude as a
  prompt sent while it works does: Claude takes it into the running turn after
  its current step, or runs it next, and an idle session starts a turn with it.
  Messages that arrive while a prompt or message is pending wait their turn in
  order, and a message to a parked session starts it. A message to an archived
  session, or to a session whose Claude cannot be launched, is refused, and the
  sending Claude gets the reason. Messages Claude has not taken are dropped if
  Claude fails to start, when Claude exits, or when `oxd` stops; a failed start
  shows why in the transcript.
- **Child reports.** When a child session's turn ends, its parent receives the
  child's final reply, or why the turn failed, as a report, without either
  session sending a message. Reports wait until the parent is idle or parked and
  none of its children is working, then arrive together as one message, after
  any message already waiting, each headed with its child's label and rendered
  as Markdown; a child waiting on the user does not hold them back. A report
  carries only the first 400 characters of a longer reply, none is sent when the
  child's last action was a message to its parent, and reports not yet delivered
  are dropped when `oxd` stops.
- **Messages stay in Ox.** Claude messages only Ox's own sessions, and only
  through Ox. Other Claude Code sessions on this machine cannot message an Ox
  session.

## Persistence and parking

- **Surviving restarts.** Workspaces, sessions, their settings, and their
  transcripts survive parking and `oxd` restarts.
- **Resuming the conversation.** A parked session resumes its Claude
  conversation on the next send or message.
- **Database failures.** Adding a workspace shows a failure to save in its
  dialog, and removing one in the session list; any other failure to save goes
  to `oxd`'s log. A session whose conversation could not be saved still resumes
  it until `oxd` restarts.
- **Lost conversation.** If Claude can no longer resume the conversation, the
  prompt is dropped with a notice in the transcript and its text returns to the
  composer, and the next send starts a new conversation without the earlier
  context.
- **Parking.** An idle session parks after five minutes; the period is
  configurable, and parking can be turned off. It does not park while a prompt,
  setting change, approval, background task, or schedule is pending.
- **Parked schedules.** A parked session runs no schedules until the next prompt
  or message starts it.

## Archiving and deleting

- **Deleting a session.** A session, archived or not, can be deleted only when
  it has no child sessions. Deleting it asks first, then stops Claude, deletes
  Ox's history of it, leaves Claude's transcript, Ur's saved conversation, and
  the workspace on disk, and removes it from every page.
- **Archiving a session.** While a session and every session beneath it are idle
  or parked, the user can archive it from its menu without confirmation, which
  archives the sessions beneath it too and stops their Claude processes,
  schedules, and background tasks. Unarchiving a session unarchives the sessions
  beneath it; a child can be archived without its parent, but not unarchived
  while its parent is archived.
- **Archived sessions.** An archived session keeps its transcript and shows it
  with an Unarchive button in place of the composer and settings, refuses
  prompts, messages, and setting changes, and stays archived through `oxd`
  restarts and on every page. Its unsent text returns when it is unarchived.
- **Showing archived sessions.** A workspace's archived sessions are hidden
  until the user shows them from the workspace's menu, a choice that lasts until
  the page reloads; shown, they keep their place, marked as archived. An
  archived session's URL opens it even while it is hidden.

## Pages and connection

- **Every page in sync.** Changes made from another page, to workspaces,
  sessions, settings, or a conversation, appear on every page as they happen.
- **Actions show at once.** Each action shows its result on the page as soon as
  the user takes it, and adding a workspace as soon as Ox has checked the
  directory. If Ox refuses an action, the page goes back to how things stand and
  shows why where the action was taken.
- **Every request settles.** Every request ends in its result or an error, shown
  in the view that made it. The error stays until that view's next request, even
  if a later request finishes first.
- **Lost connection.** When the connection drops, the page says so, fails its
  pending requests, and holds Send and settings until it has caught up. It
  reconnects by itself, unless `oxd` is still down when it tries, in which case
  it shows a Reload button.
- **Work outlives the page.** Closing every page leaves prompts, approvals, and
  Claude's own turns running.
- **Web access.** The web UI answers only requests addressed to Ox's local or
  configured hosts. When the user configures `tailscale_ip`, Ox also serves the
  web UI on that Tailscale address, at a port that stays the same across
  restarts and local port changes, and prints that URL at startup.
- **Home Screen app.** On an iPhone, Ox added to the Home Screen from Safari
  shows its icon and the name Ox, and opens full screen without Safari's
  toolbars, at the session list, over the same address the browser uses.

## Layout

- **Panel toggles.** Two buttons show and hide the session list and the file
  tree, each showing whether its panel is shown.
- **Resizing panels.** On a wide screen, dragging the boundary beside the
  session list, the file tree, or between the transcript and the file viewer
  resizes them.
- **Layout through navigation.** Which panels show and their widths are the same
  for every session. Each session and new-session draft keeps its own split
  between the transcript and the file viewer, and a draft's split carries to the
  session it creates. The layout lasts through switching views and reconnecting;
  reloading the page restores the default layout.
- **Narrow screens.** On a narrow screen, such as a phone's, the page shows one
  pane at a time: the transcript, or the open file in its place until its tab is
  closed. The session list and the file tree open over it from buttons in the
  session's header, and close when the user taps outside them, picks a session
  or a new-session draft, or opens a file; panels cannot be resized. At load,
  the session list is open when the URL names no session, and closed otherwise.
- **Touch screens.** On a touch screen, session and file rows are tall enough to
  tap, controls that appear on hover elsewhere always show, focusing a text
  field does not zoom the page, and the page keeps its controls clear of the
  notch, rounded corners, and browser toolbars.

## Files

- **File tree.** Beside the transcript, a panel shows the directory the selected
  session works in, or a new-session draft's workspace directory, as a tree,
  folders first, whose folders expand and collapse. It hides version-control
  folders and operating-system metadata files at any depth, such as `.git` and
  `.DS_Store`, shows files Git ignores, lists nothing outside that directory,
  and shows why a folder cannot be read.
- **File tree updates.** While the page is visible, the tree shows changes on
  disk to the folders it shows within about a second.
- **File viewer.** Clicking a file in the file tree opens it beside the
  transcript, or in its place on a narrow screen, under a tab with its name that
  closes it, and marks its row; clicking another file replaces it. The pane
  shows the file's text, or why, when the file is binary, larger than 1 MiB, not
  UTF-8 text, or cannot be read, and reads nothing outside the file tree's
  directory.
- **File viewer updates.** While the page is visible, the open file shows
  changes on disk within about a second; a file deleted or renamed keeps its tab
  and shows why it cannot be read until it returns.
- **Syntax highlighting.** The file viewer colors a file's text by its language,
  chosen from the file's name, in colors that follow the page's light or dark
  theme; a file whose language Ox does not recognize shows as plain text.
- **Line numbers.** The file viewer numbers each line of a file's text in a
  gutter that stays in view as the text scrolls sideways, and copying text from
  the pane copies none of the numbers.
- **Word wrap.** A button above the file's text wraps long lines to the pane's
  width and turns wrapping off again. The choice holds for every file the page
  opens until the page reloads, and starts off.
- **Markdown preview.** A Markdown file opens rendered, as the transcript
  renders Markdown, and a button above it switches between the rendering and its
  source. The choice lasts while the file stays open in its session, through
  switching views and reconnecting.
- **Open file.** Each session, and each workspace's new-session draft, keeps its
  open file through switching views and reconnecting, and a draft's open file
  stays open in the session it creates; reloading the page closes it.
- **File tree expansion.** Each session keeps which folders are expanded through
  switching views and reconnecting; reloading the page collapses them.

## Claude Code

- **Failed startup.** A failed Claude startup ends within a minute and shows
  why, in Claude's own words when it gives a reason. The prompt that started it
  is dropped, and its text returns to the composer of the page that sent it.
- **Working directory.** Claude works in the session's workspace directory, or
  in a child session, the directory it was created in.
- **Subscription billing.** Claude's work is billed to the user's Claude
  subscription, never to an API key or token in Ox's environment.
- **Subscription usage.** Under the composer, a Claude session shows how much of
  its shortest subscription usage limit is used and how long until it resets;
  clicking it lists every limit Claude reports. The session shows the reading
  from its latest turn, through parking and restarts, dimmed once its limit has
  reset, and shows nothing before Claude has reported one.
- **Ox's instructions.** Claude knows that it is an Ox session, which one, and
  how to reach the other sessions, and follows Ox's style.
- **Claude's workspace access.** Claude changes the workspace only as its
  permission mode and the user's approvals allow.

## Ur

- **Ur sessions.** Ur chats through OpenRouter with the user's
  `OPENROUTER_API_KEY`, streaming its replies; a send to an Ur session without
  the key is refused with an error. An Ur session parks and resumes its
  conversation like a Claude session, including a reply cut off partway. If its
  saved conversation can no longer be read, the send is refused with an error
  saying so, and the next send starts a new conversation without the earlier
  context.
- **Ur's shell commands.** Ur can run shell commands in the session's workspace
  directory, without `OPENROUTER_API_KEY` in their environment. In manual mode
  each command asks for approval first, and a denied command tells Ur so and the
  turn goes on; in auto mode every command runs without asking. A command ends
  when it exits, at its timeout of two minutes or up to ten that Ur sets, or
  when the turn is stopped, which also ends the processes it started; Ur gets
  its exit status and its output, or the start and end of output too long to
  keep whole.
- **Ur's instructions.** Ur knows the session's workspace directory, its OS, and
  today's date, follows Ox's style, and follows the workspace's `AGENTS.md` as
  it stands when each request is sent.

## Configuration

- **Built-in catalog.** When Ox starts without a configuration file, it writes
  one for the user to edit, offering the Claude and Ur models Ox supports with
  their effort levels and permission modes.
- **Titling model.** The user chooses in the configuration the harness, model,
  and effort that title sessions, from those Ox offers.
- **Configuration checks.** `oxd` refuses to start with a configuration that is
  malformed or that it cannot honor, and says why.
- **File locations.** Ox reads its configuration and keeps its database, socket,
  and Ur's saved conversations in the user's XDG directories, and the user can
  choose other paths.

## oxd

- **Independent sessions.** One session starting, changing settings, or running
  never holds up another session.
- **Stopping oxd.** Stopping `oxd` stops every Claude process and Ur command it
  runs; a second stop request ends it at once.
- **Socket safety.** After `oxd` exits, even by crashing, it starts again.
  Starting a second `oxd` while one runs fails and leaves the running one alone,
  and `oxd` never overwrites another file at its socket's path.
- **Ox's own files.** Ox's own bookkeeping writes only its database, its socket,
  the configuration it writes on first start, and Ur's saved conversations.
