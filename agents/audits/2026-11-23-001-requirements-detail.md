# Requirements detail audit

## Scope and evidence

This audit reads `docs/functional-requirements.md` at `e332d95` and asks of
each phrase whether the person using Ox would report its breaking as a bug. It
checks the phrases it questions against the code and tests that own them:
`cmd/ox/web/src` (`Panels.tsx`, `Sidebar.tsx`, `SessionView.tsx`,
`SettingsFields.tsx`, `FileTree.tsx`, `FileView.tsx`, `style.css`),
`internal/config/default.json`, `internal/server/files.go`,
`internal/server/mcp.go`, `internal/ur/shell.go`, `cmd/ox/main.go`, and their
tests. It counts how often a phrase has changed with `git log -G` on the
document.

The uncommitted `tidy-transcript-spacing` work in the main checkout edits
**Tool calls**, **Reasoning**, **Following output**, and **Layout through
navigation**. Its edit to the last one changes the default panels at reload,
a designer's choice the Loosen below removes from the document.

Gaps: no test was run. Nothing here edits the requirements; the user owns
them.

## Proposed rule

Text for the Documentation section of `AGENTS.md`:

> A requirement states what the user sees and relies on, in the words they
> would use to report it broken: outcomes, the limits they hit, the data that
> survives, what is refused and why, the order they depend on, and the
> boundaries Ox keeps. It leaves to the code and tests the values a designer
> or maintainer could change without the user calling it a regression, such
> as sizes, breakpoints, colors, placement, catalogs, protocol names, and
> internal states.

The test for a borderline phrase: if a maintainer changed it on purpose,
would the requirement need the user's approval? A fixed port the user
bookmarked, yes; a breakpoint moved from 768 px to 800 px, no.

## Findings

Cut removes the phrase. Loosen keeps the promise in the user's terms. Keep
lists phrases that look like detail but carry a promise. Requirements not
listed are fine as written.

| Requirement | Phrase | Why | Action | Proposed text |
| ----------- | ------ | --- | ------ | ------------- |
| Collapsing child sessions | "shows a chevron after its title while its row is hovered or focused" | Icon and reveal-on-hover are styling (`Sidebar.tsx`, `style.css`). The promise is that a row offers the control. | Loosen | "A session with child sessions has a control on its row that hides or shows every session beneath it; clicking elsewhere on the row opens the session." Rest unchanged. |
| Stop | "Stop shows beside Send" | Placement. | Loosen | "While Claude works, a Stop button shows." |
| Reasoning | "a collapsed Thinking row" | UI copy and default disclosure state, which the pending `tidy-transcript-spacing` change already alters. | Loosen | "When Claude or Ur reasons, the transcript shows the reasoning where it happened, streaming in as Markdown under the same rules as replies." |
| Permission requests | "as a card above the composer" | Placement. | Loosen | "A tool permission request appears in its session on every page, including after a reload, until it is answered or withdrawn." |
| Allow and Deny | "exactly as the card shows it" | Names the widget. | Loosen | "Allow approves the tool's input exactly as shown". |
| Claude can read other sessions | "a page at a time with the newest last" | Paging shape of the `read_session` tool (`mcp.go`); Claude's concern, not the user's. | Cut | "Claude can read any other session's transcript and current status, in any permission mode, without asking for approval." |
| Claude can start child sessions | "with a name, a title, and a first message" | The name is a tool argument the user never sees; the title becomes the child's label. | Loosen | "…can start a child session with a title and a first message…" |
| Database failures | "Saved state, including transcripts, persists only while Ox can write its database. `oxd` logs a failed write of a transcript or a setting, and the page does not show it" | States what Ox lacks. The user-facing facts are where each failure shows and what still resumes. | Loosen | "Adding or removing a workspace shows a failure to save in its dialog; any other failure to save goes to `oxd`'s log. A session whose conversation could not be saved still resumes it until `oxd` restarts." |
| Showing archived sessions | "dimmed, with an archived marker in place of their status" | Styling. | Loosen | "shown, they keep their place, marked as archived." |
| Lost connection | "until each session has reloaded" | Internal reload of session state (`ox.tsx`). | Loosen | "holds Send and settings until it has caught up." |
| Web access | "The web relay accepts requests addressed to Ox's local or configured hosts" | "Web relay" is a component name (`cmd/ox`). The boundary stays. | Loosen | "The web UI answers only requests addressed to Ox's local or configured hosts." |
| Web access | "at port 17840, independently of the local listener's port" | The number lives in `cmd/ox/main.go:29` and `main_test.go`. The user relies on the address staying the same, and Ox prints it. | Loosen | "…on that Tailscale address, at a port that stays the same across restarts and local port changes, and prints that URL at startup." |
| Panel toggles | "each in full color while its panel shows and dimmed while it is hidden" | Styling. The promise is that each shows its state. | Loosen | "Two buttons show and hide the session list and the file tree, each showing whether its panel is shown." |
| Panel toggles | "On a screen 768 px or wider, they sit at the right of the file tree's header while the tree shows, and otherwise at the right of the session's header, whether or not a file is open." | Placement, owned by `App.tsx` and `style.css`. | Cut | delete |
| Resizing panels, Layout through navigation, Narrow screens | "768 px" (four times) | The breakpoint is `narrowQuery` in `Panels.tsx`, a value a designer tunes. The user knows "phone" and "desktop", not pixels. | Loosen | "On a wide screen" / "On a narrow screen, such as a phone's". |
| Layout through navigation | "reloading the page on a screen 768 px or wider shows both panels at their default widths, with the transcript and file viewer splitting evenly" | Default layout is a design choice; the pending change already rewrites it. The promise is that reload resets the layout. | Loosen | "reloading the page restores the default layout." |
| Narrow screens | "slide in over it from the left and right, from the buttons at either end of the session's header" | Animation and placement. | Loosen | "The session list and the file tree open over it from buttons in the session's header, and close when…" Rest unchanged. |
| Touch screens | "the session list's New session, actions, and child sessions buttons always show" | Enumerates `Sidebar.tsx`'s hover-revealed buttons; a new one would silently fall outside it. | Loosen | "controls that appear on hover elsewhere always show" |
| File tree | "It hides `.git`, `.svn`, `.hg`, `.hypercube`, `.DS_Store`, and `Thumbs.db`" | Mirrors the `case` in `internal/server/files.go:15`; `methods_test.go` owns it. The list has changed twice. | Loosen | "It hides version-control folders and operating-system metadata files at any depth, such as `.git` and `.DS_Store`, shows files Git ignores…" |
| File viewer | "in a pane between the transcript and the tree, … under a tab with its name and a close button, and highlights its row" | Placement and widget anatomy. Keep the tab's name and the row highlight, which tell the user which file is open. | Loosen | "Clicking a file in the file tree opens it beside the transcript, or in its place on a narrow screen, under a tab with its name that closes it, and marks its row; clicking another file replaces it." |
| File viewer | "in a monospaced font without wrapping" | Styling (`style.css:433`). | Cut | "The pane shows the file's text, or why it cannot…" |
| File viewer | "one line saying why" | Styling. | Loosen | "or why, when the file is…" |
| Syntax highlighting | "in GitHub's light or dark colors to match the page" | Theme choice (`highlight.ts`); changed twice. The promise is following the page's theme. | Loosen | "in colors that follow the page's light or dark theme" |
| Ox's instructions | "Ox's appended instructions" | How the instructions reach Claude (`--append-system-prompt`). | Loosen | "Claude knows that it is an Ox session, which one, and how to reach the other sessions, and follows Ox's style." |
| Ur sessions | "Ur saves each prompt, each reply as far as it streamed, and each shell command's outcome" | What the save file holds (`internal/ur`). The promise is what resumes. | Loosen | "An Ur session parks and resumes its conversation like a Claude session, including a reply cut off partway." |
| Ur's shell commands | "the first and last 8 KiB of its output" | A tuning constant owned by `shell_test.go:88`. The user-facing fact is that Ur sees the ends of long output. | Loosen | "Ur gets its exit status and its output, or the start and end of output too long to keep whole." |
| Ur's instructions | "stating that it is a coding agent in Ox working through its shell tool, Ox's style, the session's workspace directory, the OS, and today's date" | Enumerates the contents of `internal/ur/instructions.go`. | Loosen | "Ur knows the session's workspace directory, its OS, and today's date, follows Ox's style, and follows the workspace's `AGENTS.md` as it stands when each request is sent." |
| Built-in catalog | "offering Claude Opus, Sonnet, and Haiku 5.5 with their effort levels and the manual, auto, acceptEdits, and plan modes, and Ur with DeepSeek V4.1 Flash and GLM 5.3 Flash at low, high, and max effort and Muse Spark 1.3 Contributor at minimal, low, medium, high, xhigh, and max effort, in manual and auto modes" | Restates `internal/config/default.json`; every catalog edit becomes a requirements edit. Changed three times. | Cut | "When Ox starts without a configuration file, it writes one for the user to edit, offering the Claude and Ur models Ox supports with their effort levels and permission modes." |
| Configuration checks | "defaults it does not offer, a maximum child depth below 1, an unknown harness, or an effort level, permission mode, or provider order its harness does not take" | Enumerates `config.go`'s validation, which `config_test.go` owns. | Loosen | "`oxd` refuses to start with a configuration that is malformed or that it cannot honor, and says why." |
| Socket safety | "A socket left by an `oxd` that has exited … a file that is not a socket" | Mechanism. The user's terms are crash recovery and a second start. | Loosen | "After `oxd` exits, even by crashing, it starts again. Starting a second `oxd` while one runs fails and leaves the running one alone, and `oxd` never overwrites another file at its socket's path." |
| Parking | "after five minutes" | Precise, but the user notices it and it is the configurable default. | Keep | — |
| Prompt size limit, File viewer | "1 MiB" | A limit the user hits. | Keep | — |
| Ur's shell commands | "two minutes or up to ten" | A limit the user hits when a build runs long. | Keep | — |
| File tree updates, File viewer updates | "within about a second" | Timing the user notices. | Keep | — |
| Replies as Markdown | "Raw HTML shows as text, images are not loaded, unsafe links are not links" | A security boundary. | Keep | — |
| Questions from Claude | "Previous and Next" | Button copy, but the move-and-keep rules are what the user relies on, and naming the buttons is the plainest way to state them. | Keep | — |
| Setting commands | "`/model` or `/effort`" | The user types these. | Keep | — |
| Session status | the list of states | Each is a state the user sees and acts on. | Keep | — |

Counts: 4 Cut and 26 Loosen across 26 requirements, and 8 Keep.

## Requirements to remove

None. Every requirement carries at least one promise the user relies on. The
nearest to pure implementation are **Socket safety** and **Configuration
checks**, which keep a sentence each once loosened.

## Behavior the document lacks

From the code at `e332d95`:

- **Menus.** A session's or workspace's menu also opens by right-clicking its
  row or header, and every unavailable action in it shows why (Archive: "Wait
  until it is idle"; Delete: "Delete its child sessions first"; Unarchive:
  "Unarchive its parent first"), not only Remove workspace
  (`Sidebar.tsx:153`, `:436`, `:331–370`). Proposed: "A session's and a workspace's
  actions open from its menu button or by right-clicking it, and an
  unavailable action shows why."
- **Model search.** The model picker filters every offered model by model or
  harness name as the user types, and the arrow keys and Enter pick one
  (`SettingsFields.tsx:29–60`). **New-session choices** says only that the
  user can pick. Proposed addition: "The user finds a model by typing part of
  its name or its harness's."
- **Session header.** A session's view shows its label and the directory
  Claude works in, which for a child may differ from the workspace
  (`SessionView.tsx:38–45`). Proposed: "A session's view shows its label and
  the directory it works in."
- **Workspace names.** The session list names each workspace by its
  directory's name, with the full path on hover (`Sidebar.tsx:435`).
  Proposed addition to **Adding a workspace**: "The session list shows it by
  its directory's name."
- **Child names.** `create_session` refuses a second child with the same name
  under one parent (`mcp.go:431`), so a Claude that retries does not start a
  duplicate child. If the user relies on that, it belongs in **Claude can
  start child sessions** in those words; otherwise the name drops as above.
