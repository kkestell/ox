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
  model writes from the first prompt with text the user sent it, which replaces
  the label everywhere it shows once it arrives and lasts through restarts.
  Until then, or if titling fails, the session is labeled with that prompt's
  first line, shortened, or a short identifier before it has one.
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
- **Worktrees.** Before sending, the user can choose to run a new session in its
  own git worktree, the choice a draft starts with, or in the workspace
  directory. Ox makes the worktree outside the repository, on a new branch from
  the workspace's current commit, holding only committed files, and runs the
  session in the worktree's copy of the workspace directory; if it cannot,
  creation fails with the reason and leaves no worktree or branch behind.
- **Creating a session.** Submitting a new session creates it and sends the
  prompt exactly once.
- **Creation keeps the user's view.** A session whose creation finishes after
  the user has moved to another view does not take them away from it.
- **Failed creation.** If creating a session fails, its text and images return
  to the draft with its settings and the error, with a way back to the draft's
  chat from any other view, and the user can submit again.
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
  draft, and an approval's unsent answers survive switching views and
  reconnecting; a question form stays on the question the user left it on.
  Reloading the page discards them, and removing a workspace discards its draft.
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
- **Stop.** While Claude works, the composer is empty, and Ox has accepted any
  prompt this page sent, the Send button becomes Stop. Once pressed, Stop does
  not return, and Send stays unavailable, until the turn it stopped has ended.
  Stop ends the turn, the transcript shows it was interrupted, and the session
  takes prompts again; a pending prompt is dropped, and its text returns to the
  composer of the page that sent it, ahead of anything typed since. Messages
  from other sessions that Claude has not taken are dropped, and their senders
  are not told.

## Transcript

- **Session header.** A session's chat tab shows its label, or "New session" for
  a draft, and the directory it works in as its tooltip.
- **Prompts as sent.** Prompts show in the transcript rendered as Markdown under
  the same rules as replies, followed by the images attached to them.
- **Messages in the transcript.** A message shows in the recipient's transcript
  rendered as Markdown under the same rules as replies, headed with the label of
  the session that sent it, pending at the end of the transcript until Claude
  takes it, on every page and after a reload.
- **Replies as Markdown.** Claude's replies stream in as Markdown, with each
  line break shown as a line break. Raw HTML shows as text, images are not
  loaded, unsafe links are not links, and links open in a new tab.
- **Tool calls.** Each tool call shows its name, a summary of its input, and
  whether it is running, done, failed, or stopped; expanding it shows the full
  input and the result's text, and a plan renders as Markdown. Expanding or
  collapsing a tool call keeps its summary where it was on screen.
- **Reasoning.** When Ur reasons, or Claude reasons with thinking summaries
  turned on in the configuration, the transcript shows a collapsed Thinking row
  where it happened; expanding it shows the reasoning in full, streaming in as
  Markdown under the same rules as replies, and keeps its summary where it was
  on screen.
- **Main conversation only.** The transcript shows the main conversation only,
  not the inner messages of Claude's subagents.
- **Failed turns.** A failed turn shows its error in the transcript.
- **Unexpected exits.** A Claude or Ur process that exits without Ox stopping it
  leaves the reason in the transcript.
- **Following output.** The transcript follows new output while it is scrolled
  to its bottom. Sending a prompt, or answering an approval or a question,
  returns it to its bottom.
- **Jump to bottom.** While the transcript is scrolled up from its bottom, a
  button over it returns it to its bottom, where it follows new output again.
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
  offers it, and otherwise takes the new model's first effort level.
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
  are not archived, with each one's label, workspace, the folder it works in,
  its worktree if it has one, settings, and status, and send any of them a
  message. It does so without asking for approval, except in plan mode, where
  each message asks first.
- **Claude can read other sessions.** Claude can read any other session's
  transcript and current status, in any permission mode, without asking for
  approval.
- **Claude can start child sessions.** A session's Claude can start a child
  session with a title and a first message, which reaches the child as a message
  from its parent, without asking for approval except in plan mode, where Claude
  asks first. The child takes its parent's permission mode, and its parent's
  harness, model, effort, and directory unless Claude picks others Ox offers,
  and can start children of its own up to the configured depth.
- **Who works in a worktree.** A worktree session's child sessions work in its
  worktree unless Claude picks another directory. No other session is started
  there: Ox refuses a child of another session in it and refuses to add a folder
  inside it as a workspace, and says why.
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
  it has no child sessions. Deleting it asks first, then stops Claude and waits
  for it to exit, deletes Ox's history of it, removes its worktree if it has
  one, leaves Claude's transcript, Ur's saved conversation, the session's
  branch, and the workspace on disk, and removes it from every page. A session
  whose worktree has uncommitted changes or untracked files, or whose worktree
  is on no branch, is not deleted, and the error says why; files Git ignores are
  deleted with the worktree, and a worktree folder already removed does not stop
  the deletion.
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

- **Panel toggles.** A button shows and hides the session list, and a button
  shows and hides the Files | Changes panel at the right, each showing whether
  its panel is shown. A button sits in its panel's header while the panel shows.
  While the session list is closed its button is at the left end of the leftmost
  top tab strip; while the panel is closed its button is at the right end of the
  rightmost top tab strip. The blank page, with no session or draft, has no
  panel and no panel button.
- **Tabs.** The center is a tab area. Each session and draft has a chat tab,
  labeled with its title, that cannot be closed; the Changes view, opened from
  the Changes list, with the total lines added and removed on its tab; and a tab
  for each open file. Clicking a file in the Files tab opens it in the group
  last used, or shows its tab. Each session and draft keeps its tabs, groups,
  and splits through switching views and reconnecting, a draft's carry to the
  session it creates, and reloading resets them.
- **Splitting tab groups.** Dragging a tab, or a file from the Files tab, onto
  the edge of a group opens a new group beside or above or below it; onto the
  middle of a group or its tab strip moves the tab into it; onto a tab places it
  there. A group whose last tab leaves or closes disappears. On a wide screen
  the boundaries between groups resize by dragging. A split beside a group is
  offered only while every group would keep its minimum width; the panels'
  boundaries stop where the groups reach it.
- **Chat status behind other tabs.** While another tab hides a session's or
  draft's chat, its chat tab is marked when the chat shows an error, a notice,
  or an approval waiting on the user, and selecting it shows them. On a narrow
  screen the tab picker carries the mark.
- **Tab actions.** The active tab's view actions sit at the right end of its
  group's tab strip: Source and Preview for a Markdown file, and a word wrap
  button for a file's source and for the Changes view.
- **Resizing panels.** On a wide screen, dragging the boundary beside the
  session list, the Files | Changes panel, or between tab groups resizes them.
- **Layout through navigation.** Whether the session list and the Files |
  Changes panel show, the panel's tab, and the side panels' widths are the same
  for every session; each session and new-session draft keeps its own tabs and
  groups (see Tabs). The layout lasts through switching views and reconnecting.
  Reloading the page restores the default layout: panel closed, only chat tabs.
- **Narrow screens.** On a narrow screen the page shows one tab at a time and
  offers no splitting or dragging. The header holds the session list's button, a
  picker showing the active tab that opens a menu of the session's tabs (the
  active one checked, each closable tab with a close button; each file's folder
  shows beside its name), and the panel's button. The session list and the Files
  | Changes panel open over the page from the left and the right; either closes
  when the user taps outside it, picks a session or draft, or opens a file;
  panels cannot be resized. At load the session list is open when the URL names
  no session. Switching between a narrow and a wide screen keeps the tabs.
- **Touch screens.** On a touch screen, session and file rows are tall enough to
  tap, controls that appear on hover elsewhere always show, focusing a text
  field does not zoom the page, and the page keeps its controls clear of the
  notch, rounded corners, and browser toolbars.

## Files

- **File tree.** The panel's Files tab shows the directory the selected session
  works in, or a new-session draft's workspace directory, as a tree, folders
  first, whose folders expand and collapse. It hides version-control folders and
  operating-system metadata files at any depth, such as `.git` and `.DS_Store`,
  shows files Git ignores, lists nothing outside that directory, and shows why a
  folder cannot be read. Each changed file, and each folder above one, is
  colored by its change: modified, added, or deleted, and modified for a folder
  whose files changed in different ways.
- **File tree updates.** While the page is visible, the tree shows changes on
  disk to the folders it shows within about a second.
- **File viewer.** Clicking a file in the file tree opens it as a tab, and marks
  its row. The pane shows the file's text, or why, when the file is binary,
  larger than 1 MiB, not UTF-8 text, or cannot be read, and reads nothing
  outside the file tree's directory.
- **File viewer updates.** While the page is visible, the open file shows
  changes on disk within about a second; a file deleted or renamed keeps its tab
  and shows why it cannot be read until it returns.
- **Syntax highlighting.** The file viewer colors a file's text by its language,
  chosen from the file's name, in colors that follow the page's light or dark
  theme; a file whose language Ox does not recognize shows as plain text.
- **Line numbers.** The file viewer numbers each line of a file's text in a
  gutter that stays in view as the text scrolls sideways, and copying text from
  the pane copies none of the numbers.
- **Word wrap.** A button in the tab strip, for a file's source and the Changes
  view, wraps long lines to the pane's width and turns wrapping off again. The
  choice holds for every file the page opens until the page reloads, and starts
  off.
- **File view modes.** A Markdown file can show its source or its rendering,
  from buttons in its tab strip, and opens rendered; any other file shows its
  source. The choice lasts while the tab stays open in its session.
- **File tree expansion.** Each session keeps which folders are expanded through
  switching views and reconnecting; reloading the page collapses them.
- **Changes view.** The panel's Changes tab lists each file in the selected
  session's directory, or a draft's workspace directory, that differs from where
  the session's branch left the workspace's branch (counting commits,
  uncommitted edits, and untracked files but not files Git ignores), with its
  folder and lines added and removed, and the branch and totals at its top.
  Clicking a file, or the top row, opens the Changes tab; the Changes tab shows
  every file's diff, and a click on a file scrolls to it. It says why when the
  directory is not in a Git repository.
- **Change diffs.** Each file in the Changes tab has a button that opens its
  file tab, unless it was deleted, and shows its changed lines with the lines
  around them in sections that collapse and start expanded, numbered, colored by
  whether they were added or removed, and highlighted as the file viewer
  highlights them. A binary file, or one whose diff is larger than 1 MiB or not
  UTF-8 text, says so instead.
- **Changes updates.** While the page is visible, the Changes list and tab, and
  the file tree's colors show changes on disk and new commits within about a
  second.

## Claude Code

- **Failed startup.** A failed Claude startup ends within a minute and shows
  why, in Claude's own words when it gives a reason. The prompt that started it
  is dropped, and its text returns to the composer of the page that sent it.
- **Working directory.** Claude works in the session's directory: the workspace
  directory, its copy in the session's worktree, or for a child session, the
  directory it was created in. A session whose directory no longer exists
  refuses to start, and says so.
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
  saved conversation can no longer be read, the prompt is dropped with a notice
  in the transcript and its text returns to the composer, and the next send
  starts a new conversation without the earlier context.
- **Ur's shell commands.** Ur can run shell commands in the session's directory,
  without `OPENROUTER_API_KEY` in their environment. In manual mode each command
  asks for approval first, and a denied command tells Ur so and the turn goes
  on; in auto mode every command runs without asking. A command ends when it
  exits, at its timeout of two minutes or up to ten that Ur sets, or when the
  turn is stopped, which also ends the processes it started; Ur gets its exit
  status and its output, or the start and end of output too long to keep whole.
- **Ur works with other sessions.** Ur can do what Claude does under Sessions
  working together: look up Ox's models, list, read, and message other sessions,
  and start child sessions, in either permission mode without asking for
  approval.
- **Ur's instructions.** Ur knows the session's directory, its OS, and today's
  date, that it is an Ox session, which one, and how to reach the other
  sessions, follows Ox's style, and follows the `AGENTS.md` in that directory as
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
  Ur's saved conversations, and session worktrees in the user's XDG directories,
  and the user can choose other paths.

## oxd

- **Independent sessions.** One session starting, changing settings, or running
  never holds up another session.
- **Stopping oxd.** Stopping `oxd` stops every Claude process and Ur command it
  runs, and undoes a worktree it is still making; a second stop request ends it
  at once, which can leave that worktree and its branch behind.
- **Socket safety.** After `oxd` exits, even by crashing, it starts again.
  Starting a second `oxd` while one runs fails and leaves the running one alone,
  and `oxd` never overwrites another file at its socket's path.
- **Ox's own files.** Ox's own bookkeeping writes only its database, its socket,
  the configuration it writes on first start, and Ur's saved conversations. In
  the user's repositories, Ox writes only the worktrees and branches of worktree
  sessions.
