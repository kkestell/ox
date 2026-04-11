# Ox — Functional Requirements

Ox is a terminal user interface (TUI) for conversing with large language models. It provides a chat interface where the user types natural-language messages and the assistant responds with text, optionally invoking tools (file operations, shell commands, sub-agents) to accomplish tasks on the user's behalf. Ox is a presentation layer over the Ur agent framework; this document specifies what the user sees and can do in the TUI.

The name "Ox" is a reference to the artificial consciousness in Frank Herbert's *Destination Void*.

Screenshots referenced throughout are in `.ox/screenshots/`. They capture a single end-to-end interaction and are ordered chronologically by filename timestamp.

---

## 1. Application Lifecycle

### 1.1 Configuration Phase

Before the TUI launches, Ox runs a plain-text console phase to ensure the system is ready. Two things must be configured: a **model** and an **API key** for that model's provider.

- **No model selected:** The user is prompted to enter a model ID in `provider/model` format (e.g. `openai/gpt-4o`, `google/gemini-3-flash-preview`, `ollama/llama3`). Entering a blank line exits the application.
- **Unknown provider:** If the provider prefix is not recognized, the user is re-prompted with guidance.
- **Missing API key:** If the selected provider requires an API key and none is stored, the user is prompted to enter one. Entering a blank line exits the application.

Once both model and key are satisfied, the TUI starts. On subsequent launches with valid configuration, the TUI starts immediately with no prompts.

### 1.2 TUI Initialization

When the TUI starts, the terminal switches to an alternate screen buffer. A new session is created. The input area receives focus immediately.

### 1.3 Session Exit

The user may exit the application by:

- Pressing **Ctrl+C** at any time.
- Pressing **Ctrl+D** when the input field is empty.
- Typing `/quit` and pressing Enter.

---

## 2. Screen Layout

The screen is divided into two fixed regions:

```
┌──────────────────────────────────────────┐
│                                          │
│           Conversation Area              │
│        (fills available space)           │
│                                          │
│                                          │
├──────────────────────────────────────────┤  ← Permission prompt floats here
│            Input Area (5 rows)           │     when active (overlays bottom
└──────────────────────────────────────────┘     of conversation area)
```

> **See:** `screen-20260410-203323-713.txt` — the initial empty state showing both regions.

### 2.1 Conversation Area

Fills all vertical space above the input area. Displays the conversation as a scrollable list of entries. A vertical scrollbar appears on the right edge when content exceeds the viewport height.

> **See:** `latest.txt` — scrollbar visible on the right edge after enough content accumulates.

### 2.2 Input Area

Fixed at 5 rows, anchored to the bottom of the screen. Composed of:

| Row | Content |
|-----|---------|
| 0 | Top border (rounded box-drawing characters: `╭─╮`) |
| 1 | Text input field |
| 2 | Horizontal divider |
| 3 | Status line |
| 4 | Bottom border (rounded box-drawing characters: `╰─╯`) |

> **See:** `screen-20260410-203323-713.txt` — the input area at rest, showing the model ID on the status line and empty text field.

### 2.3 Permission Prompt

A 3-row floating panel that appears between the conversation area and the input area when a tool requires approval. It overlaps the bottom of the conversation area rather than displacing the layout. See §6 for details.

> **See:** `screen-20260410-203347-913.txt` — the permission prompt floating above the input area.

---

## 3. Splash Screen

When the conversation is empty (no messages have been exchanged), the conversation area displays a centered ASCII-art logo:

```
▒█▀▀▀█ ▀▄▒▄▀
▒█░░▒█ ░▒█░░
▒█▄▄▄█ ▄▀▒▀▄
```

The logo is rendered in dark gray. It disappears permanently as soon as the first conversation entry is added.

> **See:** `screen-20260410-203323-713.txt` — logo centered in the conversation area.
> **See:** `screen-20260410-203324-959.txt` — logo still visible while the user types (before submission).
> **See:** `screen-20260410-203326-289.txt` — logo gone after the first message is submitted.

---

## 4. Conversation Display

### 4.1 Entry Types

Each entry in the conversation has a style that determines its visual treatment:

#### User Messages

- Prefixed with a blue circle: `● `
- Text is white.
- Wrapped lines are indented to align with the first character after the circle.

> **See:** `screen-20260410-203326-289.txt` — `● Hello!` as the first user message.
> **See:** `screen-20260410-203346-792.txt` — `● Write foo to bar.txt` as a second user message.

#### Assistant Text

- Prefixed with a white circle: `● `
- Text is white.
- Streams in incrementally as the model generates tokens (character-by-character appearance).

> **See:** `screen-20260410-203353-601.txt` — partial streaming text `● OK` mid-generation.
> **See:** `screen-20260410-203353-642.txt` — completed text `● OK. I've written "foo" to bar.txt.`

#### Tool Calls

- Prefixed with a colored circle: `● `
- Circle color changes over the tool's lifecycle:
  - **Yellow** — tool has started or is awaiting approval.
  - **Green** — tool completed successfully.
  - **Red** — tool completed with an error.
- The tool signature is displayed in dark gray using a function-call format:
  ```
  ● Write("bar.txt", "foo")
  ```
- Tool argument values are truncated to 40 characters with `...` appended if longer.
- Newlines within arguments are collapsed to spaces.

> **See:** `screen-20260410-203347-913.txt` — `● Write("bar.txt", "foo")` with a yellow circle (awaiting approval).
> **See:** `screen-20260410-203352-957.txt` — the same entry after approval, now green with a result appended.

#### Tool Results

Appended below the tool signature, indented to align:
```
● Write("bar.txt", "foo")
  └─ Wrote 3 bytes to bar.txt
     (continuation lines indented)
```

- Result text is dark gray.
- A maximum of 5 result lines are shown. If the result exceeds 5 lines, a footer reads: `(N more lines)`.
- The `todo_write` tool suppresses its result display on success (the plan block in the signature is sufficient). Errors are always shown.

> **See:** `screen-20260410-203352-957.txt` — `└─ Wrote 3 bytes to bar.txt` beneath the tool signature.

#### Plan Updates (todo_write)

When the agent updates its plan, the tool call is rendered as a multi-line plan block instead of the standard function-call format:

```
● Plan
  ✓ Set up project structure
  ● Implement feature X
  ○ Write tests
```

Status markers: `✓` completed, `●` in progress, `○` pending.

#### Subagent Calls

When the agent spawns a sub-agent, the tool call entry acts as a container:

- The parent entry shows the `Subagent(...)` signature with a yellow circle (turns green on completion).
- All events from the sub-agent (text, tool calls, results) appear as **child entries** indented beneath the parent by 2 columns.
- Child entries follow the same styling rules as top-level entries.

#### Error Entries

- Prefixed with a red circle: `● `
- Text is red, formatted as: `[error] {message}`

#### Cancellation Entries

When the user cancels a turn (see §5.4), a plain `[cancelled]` entry is added.

### 4.2 Entry Spacing

A blank line separates consecutive non-plain entries (user messages, assistant text, tool calls). This creates visual rhythm between distinct conversation events.

> **See:** `screen-20260410-203326-460.txt` — blank lines separating user message from assistant response.

### 4.3 Horizontal Padding

Top-level entries have a 1-column gutter on each side.

### 4.4 Text Wrapping

- Text wraps at word boundaries (space characters).
- If a word is longer than the available width, it hard-breaks at the column limit.
- Trailing newlines are trimmed from all entries.
- Explicit newlines within text create line breaks.
- Wrapped continuation lines are indented to match the content start (past the circle prefix).

### 4.5 Scrolling

- The conversation auto-scrolls to the bottom as new content arrives.
- If the user manually scrolls up, auto-scroll is disabled.
- If the user scrolls back to the bottom, auto-scroll re-engages.

> **See:** `latest.txt` — scrollbar visible, content extends beyond the viewport.

---

## 5. User Input

### 5.1 Text Submission

The user types a message in the input field and presses **Enter** to submit. The field clears immediately after submission. The message appears in the conversation as a user entry, and a turn begins.

The input field remains editable during an active turn. Text typed while the agent is working is queued and submitted as the next turn after the current one completes.

> **See:** `screen-20260410-203324-959.txt` — `Hello!` typed in the input field, not yet submitted.
> **See:** `screen-20260410-203326-289.txt` — after submission: input field cleared, message in conversation, turn running.
> **See:** `screen-20260410-203346-636.txt` — `Write foo to bar.txt` typed in the input field before submission.

### 5.2 Slash Commands

Messages beginning with `/` are interpreted as commands rather than chat messages.

#### Built-in Commands

- `/quit` — Exits the application immediately.
- `/clear` — Clears the conversation history (planned).
- `/model` — Changes the active model (planned).
- `/set` — Modifies a setting (planned).

Built-in commands are handled locally and do not produce an LLM turn.

#### Skill Commands

If the command name does not match a built-in, the system checks for a matching user-defined skill. Skills expand into prompt templates that are injected into the conversation. See §9.

#### Unknown Commands

If neither a built-in command nor a skill matches, the user sees: `Unknown command: {name}`.

### 5.3 Autocomplete

When the input buffer matches the pattern `/<letters>` (a slash followed by one or more letters with no trailing space):

- The system searches built-in commands first, then registered skills, for names starting with the typed prefix.
- The first match's remaining characters are offered as ghost text (visually hinted after the cursor).
- Pressing **Tab** appends the completion and moves the cursor to the end.
- If the buffer is already an exact match or no match exists, Tab does nothing.

### 5.4 Turn Cancellation

Pressing **Escape** during an active turn cancels it. The agent loop stops, a `[cancelled]` entry appears in the conversation, and the system returns to idle. Escape does **not** close the session.

### 5.5 Session Close

- **Ctrl+C** — Closes the session and exits at any time.
- **Ctrl+D** — Closes the session and exits, but only when the input buffer is empty.

---

## 6. Permission System

Tools that modify files or execute commands require explicit user approval before they run.

### 6.1 Permission Policy

| Operation | In Workspace | Outside Workspace |
|-----------|-------------|-------------------|
| **Read** (read_file, glob, grep) | Auto-allowed, no prompt | Prompts (once only) |
| **Write** (write_file, update_file) | Prompts with full scope options | Prompts (once only) |
| **Execute** (bash, run_subagent) | Prompts (once only) | Prompts (once only) |

### 6.2 Permission Prompt

When a tool requires approval, a 3-row floating panel appears above the input area:

```
╭─────────────────────────────────────────────────────────────────────────────────────────╮
│ Allow 'write_file' to Write 'bar.txt'? (y/n [once, session, workspace, always]):        │
╰─────────────────────────────────────────────────────────────────────────────────────────╯
```

The prompt shows:
- The tool name (internal API name, e.g. `write_file`)
- The operation type (Read, Write, or Execute)
- The target path (workspace-relative when possible)
- Available scopes in brackets (varies by operation and location)

Focus transfers to the permission prompt's inline text field. The main input area is blurred.

> **See:** `screen-20260410-203347-913.txt` — permission prompt appears with the tool call visible above it.
> **See:** `screen-20260410-203352-034.txt` — user has typed `y` in the prompt's text field.

#### User Responses

- **`y`** or **Enter** — Approve. If multiple scopes are available, the default is the first listed scope.
- Typing a scope name (e.g. `session`, `workspace`, `always`) grants with that scope.
- **`n`**, **Escape**, or **Ctrl+C** — Deny. The tool call fails with a "Permission denied" result.

After the prompt resolves, it disappears and focus returns to the input area.

### 6.3 Permission Scopes

| Scope | Duration | Persisted |
|-------|----------|-----------|
| **Once** | This single invocation only | No |
| **Session** | All invocations in the current session | No |
| **Workspace** | All sessions in this workspace | Yes |
| **Always** | All workspaces globally | Yes |

Once a durable grant (Workspace or Always) exists, the user is not prompted again for matching operations.

### 6.4 Grant Matching

Grants use prefix matching on file paths. A grant for a directory covers all files beneath it.

---

## 7. Status Line

The input area's status line (row 3) displays two pieces of information:

### 7.1 Activity Indicator (Left-aligned)

When a turn is running, an animated throbber appears. It displays 8 circle glyphs (`●`) representing an 8-bit binary counter:

- Bits set to 1 render as white circles; bits set to 0 render as dark gray circles.
- The counter increments every second starting from 1.
- Glyphs are separated by spaces.
- The throbber is hidden when no turn is active.

> **See:** `screen-20260410-203326-289.txt` — throbber showing `● ● ● ● ● ● ● ●` (counter value 1, all bits set... wait, value 1 = rightmost bit only). The throbber pattern here is all white circles.
> **See:** `screen-20260410-203326-460.txt` — turn complete, throbber gone.
> **See:** `screen-20260410-203346-792.txt` — throbber running again during the second turn.

### 7.2 Context and Model Info (Right-aligned)

Displays the context window fill percentage and the active model ID:

```
1%  google/gemini-3-flash-preview
```

- The percentage represents how much of the model's context window is filled by the current conversation, calculated from the last turn's input token count.
- The percentage is hidden if token usage data is unavailable (e.g. before the first turn completes).
- The model ID is always shown when known.

> **See:** `screen-20260410-203323-713.txt` — model ID shown with no percentage (no turn completed yet).
> **See:** `screen-20260410-203326-460.txt` — `1%  google/gemini-3-flash-preview` after the first turn completes.

---

## 8. Tools

The agent has access to a set of tools. Users see tool invocations and results in the conversation stream. Each tool is displayed using a friendly name.

| API Name | Display Name |
|----------|-------------|
| `bash` | Bash |
| `read_file` | Read |
| `write_file` | Write |
| `update_file` | Edit |
| `glob` | Glob |
| `grep` | Grep |
| `run_subagent` | Subagent |
| `todo_write` | Plan |

Unknown tools (from extensions or MCP) are auto-converted to PascalCase by capitalizing each underscore-separated segment.

### 8.1 File Reading

- **Read** — Reads file contents, optionally with a line offset and limit.
- **Glob** — Finds files matching a glob pattern (e.g. `**/*.cs`).
- **Grep** — Searches file contents by regular expression.

### 8.2 File Writing

- **Write** — Creates or overwrites a file with specified contents. Requires permission.
- **Edit** — Finds a unique string in a file and replaces it. Requires permission.

### 8.3 Command Execution

- **Bash** — Runs a shell command. Requires permission for every invocation.

### 8.4 Sub-agents

- **Subagent** — Delegates an isolated sub-task to a fresh agent loop. The sub-agent has its own conversation and can use the same tools. Events from the sub-agent are displayed as nested entries beneath the parent call. Requires permission.

### 8.5 Task Tracking

- **Plan** — Manages a list of task items with statuses (pending, in progress, completed). Displayed as a plan block in the conversation. Auto-allowed (no permission prompt).

### 8.6 Skills

- **Skill** — Invokes a registered skill by name with optional arguments. Available to the agent to invoke programmatically (distinct from user slash commands).

---

## 9. Skills

Skills are user-defined prompt templates that extend the agent's behavior.

### 9.1 Invocation

- **By the user:** Typing `/skillname [args]` as a slash command. The skill's template is expanded and injected into the conversation.
- **By the agent:** The agent can invoke skills via the Skill tool during a turn.

### 9.2 Visibility

- **User-invocable** skills appear in autocomplete and can be typed as slash commands.
- Skills may be restricted from agent invocation.

---

## 10. Turn Lifecycle

A "turn" is one round-trip: the user submits a message, and the agent responds.

### 10.1 Normal Flow

1. User submits a message.
2. The message appears as a user entry in the conversation.
3. The activity indicator starts animating.
4. The agent streams its response (text appears incrementally).
5. If the agent invokes tools:
   a. Tool call entries appear with yellow circles.
   b. If permission is required, the prompt appears and the turn pauses.
   c. On approval, the tool executes and its result appears beneath the signature.
   d. The circle turns green (success) or red (error).
   e. Steps 4-5 may repeat (the agent may call more tools or produce more text).
6. The turn completes. The activity indicator stops. Context fill percentage updates.

The screenshots capture this entire flow end-to-end:

| Screenshot | State |
|------------|-------|
| `screen-20260410-203323-713.txt` | Empty — splash screen, input at rest |
| `screen-20260410-203324-959.txt` | User typing `Hello!` in input field |
| `screen-20260410-203326-289.txt` | Turn running — message submitted, throbber active, splash gone |
| `screen-20260410-203326-460.txt` | Turn complete — response `Hello! How can I help you today?`, context 1% |
| `screen-20260410-203346-636.txt` | User typing `Write foo to bar.txt` |
| `screen-20260410-203346-792.txt` | Second turn running — throbber active |
| `screen-20260410-203347-913.txt` | Tool call started — permission prompt floating above input |
| `screen-20260410-203352-034.txt` | User typing `y` in permission prompt |
| `screen-20260410-203352-957.txt` | Tool completed — result shown, turn continues |
| `screen-20260410-203353-601.txt` | Assistant streaming partial text `OK` |
| `screen-20260410-203353-642.txt` | Turn complete — full response, idle |
| `latest.txt` | Final state — scrollbar visible, all entries rendered |

### 10.2 Parallel Tool Execution

The agent may request multiple tool calls simultaneously. Each tool call gets its own entry and proceeds through the permission/execution lifecycle independently.

### 10.3 Error Handling

- **Tool failures:** The tool returns an error result to the agent (red circle). The turn continues — the agent can attempt recovery.
- **LLM API errors:** A fatal error entry appears in red. The turn ends.
- **Permission denial:** The tool call fails with "Permission denied." The agent sees this as a tool error and can adjust.

### 10.4 Cancellation

The user can press Escape to cancel the current turn at any point. The agent loop stops, a `[cancelled]` entry appears, and the system returns to idle.

---

## 11. Color Scheme

The application uses a minimal color palette on a black background:

| Element | Color |
|---------|-------|
| Background | Black |
| Normal text / Assistant text | White |
| User message circle | Blue |
| Tool call signature / Tool results | Dark gray |
| Tool circle (started / awaiting) | Yellow |
| Tool circle (success) | Green |
| Tool circle (error) / Error text | Red |
| Splash logo | Dark gray |
| Borders / Chrome | Light gray |
| Internal dividers | Darker gray |
| Status line model/context text | Gray |
| Throbber inactive bits | Dark gray |
| Throbber active bits | White |
