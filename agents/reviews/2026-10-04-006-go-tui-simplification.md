# Go TUI simplification review

## Scope and coverage

Reviewed the Go terminal-client port in `server/internal/tui/`,
`server/internal/client/`, and `server/cmd/ox/`, including supporting settings,
control-character escaping, server session operations, and tests. Compared the
Rust composer's editing rules and transcript rendering in Git history. The focus
was existing alternatives and behavior changes that could remove substantial
code.

This report records the preceding review. The user requested a write-up, so no
implementation changes were made. Library capabilities were checked against
upstream documentation and installed dependency sources; replacements were not
prototyped or measured. This was not a complete correctness, security, or
performance audit, and terminal end-to-end tests were not run.

## Fixed

None.

## Findings

### Medium

#### Concurrency

- **Every RPC pauses all UI events** (`server/internal/tui/tui.go:85`):
  `request()` sets `waiting`, and `Update()` buffers every message until the
  result arrives. Quit, resize, focus, permissions, and streaming updates all
  wait behind session-list and configuration requests. A slow request makes the
  interface unresponsive. This follows directly from the unconditional queue
  branch; requests after startup use `context.Background()`. Serialize only
  conflicting session operations, process ordinary UI and server events
  immediately, and identify completion messages by their originating session.
  Loading needs separate handling because the server sends replay before its
  response: accumulate replay in a pending transcript and install it on success.
  This can remove the general deferred-message queue and its draining loop.
  Needs a plan to establish replay ownership, completion ordering, and stale
  response handling without losing updates.

#### Dependencies

- **Ox owns a complete terminal Markdown renderer**
  (`server/internal/tui/markdown.go:33`): The 472-line file uses Goldmark for
  parsing but implements terminal rendering for nested styles, lists, links,
  code blocks, HTML, and tables. Keeping these rules makes Markdown changes
  materially more expensive. [Glamour](https://github.com/charmbracelet/glamour)
  provides terminal rendering, custom styles, and width-aware wrapping. Its
  [`WithPreservedNewLines` option](https://github.com/charmbracelet/glamour/blob/main/glamour.go)
  also avoids rewriting each newline into a Markdown hard break. Replace the
  visitor with a configured renderer and use a separate stylesheet for dim
  thinking text. This could remove most of the file. Needs a decision to accept
  differences in indentation, table borders, and heading/code spacing, followed
  by an integration plan with the styled-text replacement below.

- **Ox duplicates styled-text and cell-rendering facilities**
  (`server/internal/tui/text.go:14`, `server/internal/tui/view.go:52`): Custom
  `style`, `span`, and `line` types underpin style merging, grapheme wrapping,
  tab expansion, clipping, and manual cell drawing. Style merging is repeated in
  `patched()`, and `hanging()` reconstructs list/quote structure by inspecting
  span text. This couples layout to the Markdown renderer's representation.
  [Lip Gloss](https://github.com/charmbracelet/lipgloss) provides styling and
  layout; the already-installed `x/ansi` dependency provides grapheme-aware
  [clipping](https://github.com/charmbracelet/x/blob/main/ansi/truncate.go) and
  [wrapping](https://github.com/charmbracelet/x/blob/main/ansi/wrap.go).
  Ultraviolet's installed `StyledString` can draw styled ANSI text into a
  buffer. Use rendered ANSI strings, let the Markdown renderer own hanging
  indentation, and retain control escaping before rendering external text. This
  targets much of the 281-line `text.go` plus custom drawing; savings overlap
  with the Markdown replacement. Needs a plan for style precedence, background
  padding, cursor placement, and one consistent display-width calculation.

- **The composer reimplements an editable multiline widget**
  (`server/internal/tui/input.go:83`): The 272-line file owns cursor movement,
  deletion, logical-line navigation, wrapping, cursor placement, and scrolling.
  [Bubbles textarea](https://github.com/charmbracelet/bubbles/blob/main/textarea/textarea.go)
  supplies these facilities with configurable keybindings. It could replace most
  of the composer while slash-command handling remains a small adapter. Its
  cursor movement uses rune positions, whereas Ox's tests require joined emoji
  and combining sequences to be single editing units. Wrapping and tab display
  also differ. A separate completion hint could simplify integration further.
  Needs a decision on accepting these editing differences and an integration
  plan; retaining custom editing is justified if whole-grapheme movement and
  deletion are required.

#### Architecture

- **Submission also owns interruption and automatic restart**
  (`server/internal/client/session.go:60`, `server/internal/tui/tui.go:319`):
  Submitting during a turn fills one queued prompt, cancels the turn, and sends
  the queued prompt from `Finished()`. A second submission stays in the
  composer, while `/resume` adds another pending action through
  `resumeAfterTurn`. These interactions increase the number of states that
  cancellation and session changes must handle. An explicit rule could keep
  drafts in the composer while busy, use Escape to cancel, and allow submission
  or resume after cancellation completes. That removes `Queued`, automatic
  restart, and pending-resume coordination. Needs a product decision: the
  current automatic interruption behavior is useful and covered by tests, so
  removing it is not a behavior-preserving cleanup.

### Low

#### Simplicity

- **Unified diff headers duplicate the diff library**
  (`server/internal/tui/transcript.go:494`): `hunkHeader()` counts old/new lines
  and reproduces GNU's empty-file special case. The installed
  `udiff.UnifiedDiff.String()` already implements that formatting. Use
  `udiff.ToUnified()` or the existing result's `String()`, skip the two file
  headers, and color rows by their `@@`, `+`, and `-` prefixes. Retain clipping
  and tab expansion. No substantial plan is needed; decide whether to show or
  filter the library's additional "No newline at end of file" marker. Left open
  because this task records recommendations without implementation edits.

- **Favorites maintain a second model-list representation**
  (`server/internal/tui/picker.go:92`): Favorites have their own index list,
  insertion order, active-list flag, tab switching, and fallback when no
  favorite is offered. Tests explicitly preserve these behaviors. A single model
  list with favorites first would remove the second list and its mapping and
  switching rules. Needs a product decision to replace Favorites/All tabs and
  stop preserving the order in which favorites were added. This is a smaller
  opportunity than replacing rendering and editing infrastructure.

## Checks run

- During the preceding review, `go test ./internal/tui ./internal/client`
  passed; the TUI result was cached and the client tests executed.
- Inspected Rust implementations in Git history, Go callers and tests, server
  replay ordering, and installed `go-udiff`, `x/ansi`, and Ultraviolet sources.
- Checked upstream Glamour, Lip Gloss, and Bubbles sources for replacement
  capabilities and editing differences.
- Full `make check` and terminal end-to-end tests were skipped because the
  review and write-up did not change implementation or terminal behavior.

## Verdict

No findings fixed; five medium and two low findings remain open. The largest
opportunity is accepting modest differences from the Rust renderer and replacing
the custom Markdown and styled-text layers together. Decide separately whether
standard textarea editing and explicit interruption are acceptable. Keep the
nonblocking connection event queue, control-character escaping, and per-item
transcript cache: each has a clear purpose.
