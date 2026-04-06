# Ur Architecture Review

*Reviewed against: aider, claude-code, cline, goose, opencode, Roo-Code, continue, kilocode, OpenDevin, tabby*
*Date: 2026-04-05*

---

## Part 1: What Ur Gets Right

### Clean Layering

The subsystem boundaries are real. `AgentLoop` doesn't know about sessions. `ToolInvoker` doesn't know about individual tools. Permissions are a pure function of (operation type, workspace containment). Compare cline's 3755-line `Task` class that mixes agent loop, state, streaming, and UI concerns. `AgentLoop.cs` is 187 lines. This is a real advantage — each subsystem can be understood, tested, and modified independently.

### Permission Model

Three-tier grant persistence (Session/Workspace/Always), prefix-matched grants, and the policy-as-pure-function design are cleaner than cline's scattered auto-approve settings and competitive with opencode's rule merging. The decision to auto-allow workspace reads while always prompting for executes is the right security posture.

### Boo

The PTY-based terminal automation testing daemon is unique. No other project has this. Claude Code has no TUI tests. Aider has no interactive tests. This is an investment that compounds.

### Extension Trust Tiers

System > User > Workspace trust with per-workspace default-off for workspace extensions is more nuanced than anything else in the field. Goose has a security inspector pipeline but it's more about runtime checks. Ur's trust model is architectural -- untrusted code doesn't even load.

### AOT-First, Source-Generated JSON

`PublishAot` on the TUI, source-generated JSON contexts, and avoidance of reflection means fast startup and small binaries. TypeScript tools have 500ms+ cold starts. This matters for a CLI tool.

### Code Quality

The comments explain the "why." `LlmErrorHolder` documents the C# yield-in-try-catch constraint. `BuiltinToolFactories` explains the unified factory pattern. This is better than 90% of the codebases reviewed.

### Incremental Persistence

Messages flushed after each iteration with rollback on failure. Matches aider's crash safety. The JSONL append-only format is the right choice.

### CLI stdout/stderr Separation

Text to stdout, tool status to stderr means `ur chat "..." > output.txt` just works. No other tool gets this right as cleanly.

---

## Part 2: Bugs

Every claim below is verified against the source code with file paths and line numbers.

### B1. Permission prefix matching matches too broadly

**Source:** `PermissionGrantStore.cs:114-116`

```csharp
request.Target.StartsWith(grant.TargetPrefix, StringComparison.Ordinal)
```

A grant for `/home/user/project` matches `/home/user/projectile`. The fix is to append `Path.DirectorySeparatorChar` to the prefix before matching (or check that the target either equals the prefix or has a separator immediately after).

### B2. MacOsKeyring passes API key as command-line argument

**Source:** `MacOsKeyring.cs:31`

```csharp
Run("add-generic-password", "-s", service, "-a", account, "-w", secret, "-U");
```

The `secret` is passed as a command-line argument, visible to all users via `ps aux`. This is a genuine security vulnerability. The fix is to pipe the secret via stdin instead.

### B3. File writes are not atomic

**Source:** `UpdateFileTool.cs:70`, `WriteFileTool.cs:47`

Both use `File.WriteAllText(fullPath, ...)`, which truncates the file then writes. If the process crashes or loses power between truncate and write, the file is empty/lost. The fix is to write to a temp file in the same directory, then rename (atomic on most filesystems).

### B4. No line ending normalization in UpdateFileTool

**Source:** `UpdateFileTool.cs:69`

```csharp
content.Replace(oldString, newString, StringComparison.Ordinal)
```

Ordinal string comparison means CRLF vs LF mismatches cause silent edit failures. The model produces LF; files on Windows have CRLF. The match fails and the model gets "old_string not found." This will be a frequent failure on Windows.

### B5. Workspace.Contains doesn't resolve symlinks

**Source:** `Workspace.cs:32`

```csharp
var fullPath = Path.GetFullPath(path);
```

`Path.GetFullPath` resolves `.` and `..` but does NOT resolve symlinks or junction points. A symlink inside the workspace pointing to `/etc/passwd` would pass the `Contains` check. This is a security boundary bug.

### B6. Workspace.Contains uses case-sensitive comparison on case-insensitive filesystems

**Source:** `Workspace.cs:33`

`StringComparison.Ordinal` fails on macOS (case-insensitive by default) and Windows. A path like `/Users/foo/Workspace/file.txt` won't match `/users/foo/workspace/file.txt` even though they're the same directory.

---

## Part 3: Verified Gaps

Every claim below is verified against the source code.

### G1. The system prompt is just a skill listing [HIGH IMPACT]

**Source:** `SystemPromptBuilder.cs:29-51`

The system prompt is literally:

```
The following skills are available for use with the skill tool:

- skill_name: description -- when_to_use
```

That's it. No instructions about:
- How to use tools (which order, when to use which)
- Output formatting expectations
- How to handle errors from tools
- When NOT to use tools
- Constraints (workspace boundaries, dangerous commands)
- Code editing strategy (update_file vs write_file)
- Subagent usage guidance
- Context window management
- What constitutes dangerous behavior

Claude Code's system prompt is ~870 lines. Aider's varies per edit format with model-specific tuning. The model is flying blind. This is the single highest-impact gap to fix because it costs zero latency and dramatically improves output quality.

### G2. No max iteration count in AgentLoop [HIGH IMPACT]

**Source:** `AgentLoop.cs:57`

```csharp
while (true)
```

No iteration counter. A misbehaving model could loop forever, burning API credits. Cline, Goose, Roo-Code, and OpenDevin all have loop detection.

### G3. No retry logic in AgentLoop [HIGH IMPACT]

**Source:** `AgentLoop.cs:168-171`

LLM errors are caught and immediately surfaced as fatal errors. No retry with exponential backoff. If the provider returns 429 (rate limit) or 503 (service unavailable), the user gets an error. Every competitor has retry logic. Note: the underlying OpenAI SDK may have its own retry, but Ur doesn't add any on top.

### G4. No context compaction [HIGH IMPACT]

Nothing in the agent loop, session management, or skill system compacts conversation history. Once the context fills, the session dies. Every serious competitor (Claude Code with 5 strategies, Roo-Code with non-destructive condensing, Aider with recursive summarization, Goose with dual-level compaction, Cline with auto-condense) implements this.

### G5. Tools executed sequentially [HIGH IMPACT]

**Source:** `ToolInvoker.cs:37`

```csharp
foreach (var call in calls)
```

Five independent file reads = five sequential round-trips. Claude Code partitions tools into concurrent-safe (read-only) and serial batches, running up to 10 read-only tools in parallel.

### G6. No intelligent context selection (repo map) [HIGH IMPACT]

The model only sees what it explicitly reads via grep/glob. Aider uses tree-sitter + PageRank on a definition-reference graph. Continue uses LanceDB vector indexing. Roo-Code has codebase indexing. On a codebase larger than ~50 files, the model is flying blind, burning turns on exploration.

### G7. No git integration [HIGH IMPACT]

No auto-commits, no undo, no checkpoints, no diff tracking. Aider auto-commits after every edit with LLM-generated messages. Cline has shadow git checkpoints. Roo-Code has checkpoint/rewind. The user is on their own if an edit goes wrong.

### G8. No reflection/error recovery loop [HIGH IMPACT]

No auto-lint, auto-test, or error feedback. Aider runs the linter after every edit, feeds errors back to the LLM for automatic correction (up to 3 iterations). If an edit introduces a syntax error, nobody catches it.

### G9. Single provider (OpenRouter only) [HIGH IMPACT]

**Source:** `ChatClientFactory.cs:12-23`

Locked to OpenRouter. Users who want OpenAI direct, Anthropic direct, Ollama for local models, or Bedrock are out of luck. Every competitor supports multiple providers.

### G10. No web tools

No way to fetch documentation, search the web, or read a GitHub issue. Every competitor provides this.

### G11. No MCP support

Claude Code, Cline, Goose, Roo-Code, and OpenCode all support connecting to external tool server ecosystems via Model Context Protocol. Ur's Lua extensions are interesting but isolated.

### G12. No model-specific tuning

Aider has an 1800-line model-settings.yml with per-model optimization (edit format, temperature, system prompt structure, thinking tokens). Cline has 13 variant configurations. Ur treats every model identically.

### G13. No cost tracking

No per-message or per-session cost display. Users have zero visibility into spend. Every competitor tracks this.

### G14. No prompt caching strategy

No consideration for prompt cache boundaries (static vs dynamic content separation, cache warming). Claude Code and Aider optimize heavily for prompt caching, saving 90%+ of input token costs on providers that support it.

### G15. No loop detection

No detection of when the model calls the same tool with identical arguments repeatedly. Cline, Goose, Roo-Code, and OpenDevin all have loop detection.

### G16. No streaming tool execution

**Source:** `AgentLoop.cs:115`

Tools start only after the entire model response is received. Claude Code starts executing tools during model generation via `StreamingToolExecutor`.

### G17. Skill system is 40% implemented

**Source:** `SkillDefinition.cs:36-49`

Four fields are parsed but never enforced: `AllowedTools`, `Model`, `Context` (fork), `Paths`. The data model exists but the features don't.

### G18. No diagnostic awareness

No way to detect if edits introduced errors via IDE diagnostics. Cline captures VS Code diagnostics before and after edits.

### G19. No hook system

No lifecycle hooks for user customization. Claude Code has PreToolUse, PostToolUse, Stop, PreCompact hooks. Cline has similar hooks.

### G20. TUI uses Thread.Sleep(50) for input polling

**Source:** `Program.cs:331`

Busy-wait with 50ms sleep burns CPU. Should use proper async console input.

### G21. No logging framework

**Source:** `UrHost.cs:368` and others

The entire codebase uses `Console.Error.WriteLine` for error output. No structured logging, no log levels, no log files.

### G22. ReadFileTool reads entire file for line count

**Source:** `ReadFileTool.cs:59-64` (verified from earlier read)

`File.ReadLines` is lazy but `Count()` still enumerates every line. For a 10GB log file with offset=0 and limit=10, this reads all 10GB.

### G23. GrepTool/GlobTool .NET fallback doesn't respect .gitignore

**Source:** `GrepTool.cs:209-214`, `GlobTool.cs:122-124`

Both only exclude `.git/**`. On large codebases, `node_modules`, `build/`, `dist/` etc. produce massive useless output. The ripgrep backend handles .gitignore natively.

### G24. No fuzzy matching in UpdateFileTool

**Source:** `UpdateFileTool.cs:59-67`

Exact ordinal matching. A single whitespace difference = total failure. Aider has a 5-stage matching pipeline: exact → whitespace → elision → fuzzy → diagnostic feedback.

### G25. No error feedback for failed edits

**Source:** `UpdateFileTool.cs:64`

`throw new InvalidOperationException("old_string not found in file")` gives the model no diagnostic information to correct its approach. Aider shows "Did you mean these actual lines?" with the closest match.

### G26. No worktree isolation

Can't test risky changes in isolated copies. Claude Code has worktree isolation.

### G27. No multi-agent coordination

No coordinator mode, no agent delegation. Claude Code has coordinator mode. OpenDevin has agent delegation.

### G28. No browser automation

Cline has Puppeteer integration for web interaction.

### G29. BashTool hardcoded to /bin/bash

**Source:** `BashTool.cs:49`

Alpine Linux uses `/bin/sh`. NixOS puts bash elsewhere. Should detect the shell.

### G30. SubagentRunner accumulates text without limit

**Source:** `SubagentRunner.cs:65`

`StringBuilder` with no size limit. A verbose sub-agent producing 100K+ lines consumes unlimited memory.

### G31. ExtensionCatalog activates before persisting state

**Source:** `ExtensionCatalog.cs:117-118`

Extension is activated, then state is persisted. If persistence fails, the extension is running but not recorded. Next startup, it won't be activated.

### G32. ExtensionCatalog._gate not disposed

**Source:** `ExtensionCatalog.cs:16`

`SemaphoreSlim` implements `IDisposable` but `ExtensionCatalog` doesn't implement `IDisposable`.

### G33. UrHost doesn't implement IDisposable

No cleanup of Lua states, HttpClient, or other resources on shutdown.

### G34. No Windows keyring support

**Source:** `UrHost.cs:332`

`PlatformNotSupportedException` on Windows.

---

## Part 4: Test Coverage Gaps

- **Zero tests for `AgentLoop.cs`** -- the most critical file
- **Zero tests for `ToolInvoker.cs`** -- the permission-gated dispatch
- **Zero tests for `Workspace.Contains`** -- the security boundary
- **Zero tests for `UrHost` startup**
- **Zero tests for `ChatClientFactory`**
- **Zero tests for the TUI**
- **Zero tests for concurrent session access**

---

## Part 5: Priority Fix List

### Week 1 (highest impact, lowest effort)

1. Write a real system prompt (see Claude Code's for structure; ~870 lines)
2. Add max iteration count to `AgentLoop`
3. Add retry logic with exponential backoff to `AgentLoop`
4. Fix permission prefix matching (append trailing separator)
5. Fix MacOsKeyring security vulnerability (pipe secret via stdin)
6. Make file writes atomic (write-to-temp-then-rename)
7. Add line ending normalization to `UpdateFileTool`

### Week 2-3

8. Implement context compaction (fork subagent to summarize)
9. Add token counting to prevent context overflow
10. Add multi-provider support (OpenAI direct, Anthropic direct, Ollama)
11. Implement parallel read-only tool execution
12. Add error feedback loop for failed edits
13. Add fuzzy matching to `UpdateFileTool`

### Week 4-6

14. Build repo map (tree-sitter + PageRank or simpler alternative)
15. Add git integration (auto-commit, undo, checkpoint)
16. Add reflection loop (auto-lint, auto-test, error re-injection)
17. Add model-specific prompt tuning
18. Add MCP client support
19. Add web tools (fetch + search)
20. Add cost tracking
