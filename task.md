# Built-in Task / Todo List Tools Comparison

This document compares and contrasts the built-in task/todo list management tools across various AI coding assistant repositories.

## Summary Table

| Repository | Has Todo Tool | Tool Name(s) | Storage | Format | Multi-Agent Support |
|------------|---------------|--------------|---------|--------|---------------------|
| Claude Code | ✅ Yes | `Task` tools | File-based (JSON) | Structured | ✅ Yes (team tasks, claiming) |
| Cline | ✅ Yes | `task_progress` parameter | Markdown file | Markdown checklists | ❌ No |
| Continue | ✅ Yes | `write_checklist` | In-memory/Chat | Markdown checklists | ❌ No |
| OpenCode | ✅ Yes | `todowrite`, `task` | Session-based | JSON | ✅ Yes (permissions) |
| Roo-Code | ✅ Yes | `task_progress` | In-tool param | Markdown checklists | ❌ No |
| KiloCode | ✅ Yes | `task_progress` | In-tool param | Markdown checklists | ❌ No |
| OpenDevin | ✅ Yes | Task management | Session-based | Structured | ✅ Yes |
| Aider | ❌ No | N/A | N/A | N/A | N/A |
| Tabby | ❌ No | N/A | N/A | N/A | N/A |

---

## Detailed Analysis

### Claude Code

**Location:** `repos/claude-code/src/utils/tasks.ts`

**Architecture:**
- **Storage:** File-based JSON files in `~/.claude/tasks/{taskListId}/`
- **Locking:** Uses proper-lockfile with retry/backoff for concurrent access
- **High Water Mark:** Prevents ID reuse after deletion via `.highwatermark` file

**Task Schema:**
```typescript
{
  id: string,
  subject: string,
  description: string,
  activeForm: string?,      // present continuous form (e.g., "Running tests")
  owner: string?,           // agent ID
  status: "pending" | "in_progress" | "completed",
  blocks: string[],         // task IDs this task blocks
  blockedBy: string[],      // task IDs that block this task
  metadata: object?         // arbitrary metadata
}
```

**Key Features:**
- **Task Dependencies:** `blocks` and `blockedBy` for task relationships
- **Task Claiming:** `claimTask()` with atomic locking prevents race conditions
- **Agent Status:** `getAgentStatuses()` tracks which agents are busy/idle
- **Team Support:** Shared task lists via team name or session ID
- **Reset Capability:** `resetTaskList()` for clearing tasks when starting new swarms
- **Unassign on Exit:** `unassignTeammateTasks()` handles agent termination

**API Functions:**
- `createTask()` - Create with auto-increment ID
- `getTask()` / `listTasks()` - Read operations
- `updateTask()` - Update with locking
- `deleteTask()` - Delete with reference cleanup
- `blockTask()` - Create dependency relationships
- `claimTask()` - Atomic claim with busy/blocker checks

**Unique Aspects:**
- Built for multi-agent "swarm" scenarios
- File locking prevents concurrent write conflicts
- Teammates can share task lists across processes

---

### Cline (Focus Chain)

**Location:** `repos/cline/src/core/task/focus-chain/`

**Architecture:**
- **Storage:** Markdown file per task in task directory
- **File Watching:** Auto-detects user edits to the markdown file
- **Integration:** `task_progress` parameter available on all tools

**Format:**
```markdown
- [ ] Incomplete task
- [x] Completed task
```

**Key Features:**
- **Focus Chain:** Visual progress tracking in UI
- **User Editable:** Users can directly edit the markdown file
- **Auto Reminders:** Prompts model to update at configurable intervals
- **Progress Parsing:** Extracts completion percentage and current task
- **Cross-Session:** Persists across context window resets

**Configuration:**
```typescript
{
  enabled: boolean,
  remindInterval: number  // How often to remind (default: 6 messages)
}
```

**Unique Aspects:**
- Emphasizes user visibility and editability
- Works with auto-compact to persist across context resets
- CLI component for terminal display (`FocusChain.tsx`)

---

### Continue

**Location:** `repos/continue/extensions/cli/src/tools/writeChecklist.ts`

**Architecture:**
- **Storage:** In-memory/chat history (no persistent file)
- **Format:** Markdown checklists

**Tool Definition:**
```typescript
{
  name: "write_checklist",
  description: "Create or update a task checklist",
  parameters: {
    checklist: string  // Markdown format with - [ ] and - [x]
  }
}
```

**Key Features:**
- **Simple Design:** Single tool, single parameter
- **UI Preview:** Renders as styled checklist in interface
- **No Persistence:** Checklists live in chat history only

**Output:**
```
Task list status:
- [ ] Task 1
- [x] Task 2
```

**Unique Aspects:**
- Minimalist approach
- No file storage or state management
- Designed for visibility during conversations

---

### OpenCode

**Location:** `repos/opencode/packages/opencode/src/tool/todo.ts`

**Architecture:**
- **Storage:** Session-based (tied to conversation session)
- **Service:** Effect-based Todo.Service for state management
- **Permission System:** Explicit permission for `todowrite` tool

**Tool Definition:**
```typescript
{
  name: "todowrite",
  parameters: {
    todos: Array<Todo.Info>
  }
}
```

**Todo Info Schema:**
```typescript
{
  content: string,
  status: "pending" | "in_progress" | "completed" | "cancelled",
  priority?: "high" | "medium" | "low"
}
```

**Key Features:**
- **Dual Tools:** `todowrite` for simple lists, `task` for complex management
- **Permission Control:** Fine-grained permission configuration
- **UI Components:** Dedicated dock/panel for todo display
- **Internationalization:** Full i18n support for todo UI
- **API Endpoint:** `GET /session/:id/todo` for external access

**UI Features:**
- Collapsible todo dock in session composer
- Progress tracking (X of Y completed)
- Status indicators (pending, in-progress, completed)

**Unique Aspects:**
- Built with Effect.ts for functional error handling
- Strong typing with Zod validation
- Extensive permission configuration

---

### Roo-Code / KiloCode

**Location:** Similar `task_progress` parameter approach

**Architecture:**
- Uses `task_progress` parameter on tool calls
- Markdown checklist format
- In-tool parameter (not separate tool)

**Format:**
```xml
<task_progress>
- [ ] Incomplete item
- [x] Complete item
</task_progress>
```

**Key Features:**
- **Lightweight:** No separate storage mechanism
- **Optional Parameter:** Available on all tools
- **Prompt-Based:** System prompts encourage usage

**Unique Aspects:**
- Fork-based relationship between Roo-Code and KiloCode
- Simple parameter-based approach

---

### OpenDevin

**Architecture:**
- Session-based task management
- Integration with agent memory system
- Structured task format

**Key Features:**
- Task creation and tracking
- Agent coordination support
- Memory integration

---

### Aider & Tabby

**No Built-in Todo Tools:**

These repositories do not have dedicated task/todo list management tools:

- **Aider:** Focuses on code editing and chat-based interactions. No task tracking.
- **Tabby:** Primarily a code completion/suggestion engine. No task management features.

---

## Comparison by Feature

### Storage Approach

| Approach | Repositories | Pros | Cons |
|----------|--------------|------|------|
| File-based JSON | Claude Code | Persistent, shareable, concurrent access | Complex locking needed |
| Markdown file | Cline | User-editable, readable, git-friendly | Parsing overhead |
| In-memory/Chat | Continue, Roo-Code | Simple, no I/O overhead | Lost on session end |
| Session-based | OpenCode | Structured, API accessible | Requires service layer |

### Task Status Granularity

| Levels | Repositories |
|--------|--------------|
| 2 (pending, done) | Continue |
| 3 (pending, in_progress, done) | Claude Code, Cline, OpenCode, Roo-Code |
| 4+ (includes cancelled) | OpenCode |

### Multi-Agent Support

| Repository | Multi-Agent Features |
|------------|---------------------|
| Claude Code | ✅ Full (claiming, ownership, team lists, unassign) |
| OpenCode | ✅ Partial (permissions, session isolation) |
| OpenDevin | ✅ Partial (agent coordination) |
| Cline | ❌ None |
| Continue | ❌ None |
| Roo-Code | ❌ None |

### User Interaction

| Repository | User Can Edit | UI Visibility |
|------------|---------------|---------------|
| Cline | ✅ Yes (markdown file) | ✅ Focus Chain UI |
| OpenCode | ❌ No | ✅ Todo dock |
| Claude Code | ❌ No (API only) | ⚠️ Via tools |
| Continue | ❌ No | ✅ Checklist preview |

---

## Design Patterns Observed

### 1. Parameter-Based Approach (Cline, Roo-Code)
```
Tool call includes task_progress parameter
↓
System extracts and displays progress
↓
User sees checklist in UI
```

**Pros:** Simple, no separate tool needed
**Cons:** Tied to tool execution, can be verbose

### 2. Dedicated Tool Approach (OpenCode, Continue)
```
Agent calls todowrite/write_checklist tool
↓
Tool updates internal state
↓
UI reflects current state
```

**Pros:** Clear separation, explicit updates
**Cons:** Extra tool call overhead

### 3. Full Task System (Claude Code)
```
Agent creates/claims/updates tasks
↓
File system stores state
↓
Multiple agents can coordinate
↓
Blocking/dependencies enforced
```

**Pros:** Full project management capabilities
**Cons:** Significant complexity

---

## Recommendations by Use Case

### Single-User Simple Tracking
**Best:** Continue or Cline's Focus Chain
- Simple checklists
- Visual progress
- Low overhead

### Multi-Step Implementation Tasks
**Best:** OpenCode's todowrite
- Structured format
- Session persistence
- Clean UI integration

### Multi-Agent Coordination
**Best:** Claude Code's task system
- Claiming prevents conflicts
- Dependencies supported
- Team task lists
- Agent status tracking

### User-Managed Planning
**Best:** Cline's Focus Chain
- Users can edit markdown file directly
- Changes detected automatically
- Git-friendly format

---

## Key Takeaways

1. **Format Convergence:** Most tools use markdown checklist format (`- [ ]` / `- [x]`) for compatibility and readability.

2. **Storage Trade-offs:** File-based systems (Claude Code, Cline) enable persistence and sharing but add complexity; in-memory systems (Continue) are simpler but ephemeral.

3. **Multi-Agent Gap:** Only Claude Code has robust multi-agent task coordination; others assume single-user scenarios.

4. **UI Integration:** All tools with todo functionality provide visual feedback in their respective UIs.

5. **Permission Control:** OpenCode and Claude Code have explicit permission systems for task operations.

6. **Dependency Support:** Only Claude Code supports task dependencies (`blocks`/`blockedBy`).

7. **Extensibility:** Claude Code's schema includes `metadata` field for arbitrary extensions.