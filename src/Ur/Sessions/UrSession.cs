using System.Collections.ObjectModel;
using System.Diagnostics;
using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging;
using Ur.Configuration;
using Ur.Permissions;
using Ur.Prompting;
using Ur.Skills;
using Ur.Todo;
using Ur.Tools;

namespace Ur.Sessions;

/// <summary>
/// A single conversation session within the Ur system.
///
/// Sessions are the primary unit of interaction: they hold the message history,
/// manage persistence to JSONL files, and drive the agent loop when a user sends
/// a message. A session is created via <see cref="Hosting.UrHost.CreateSession"/> (new)
/// or <see cref="Hosting.UrHost.OpenSessionAsync"/> (resume from disk).
///
/// Architecture: The message list is mutable and shared with the agent loop.
/// As the loop produces assistant messages and tool results, they are appended
/// to <see cref="_messages"/> and periodically flushed to disk via
/// <see cref="PersistPendingMessagesAsync"/>. This "append and flush" approach
/// avoids writing the entire conversation on every token — only new messages
/// are written, and a crash loses at most the messages produced after the last
/// flush point.
/// </summary>
public sealed class UrSession : IAsyncDisposable
{
    private readonly UrConfiguration _configuration;
    private readonly SkillRegistry _skills;
    private readonly BuiltInCommandRegistry _builtInCommands;
    private readonly Workspace _workspace;
    private readonly ILoggerFactory _loggerFactory;
    private readonly SessionStore _sessions;
    private readonly Func<string, IChatClient> _chatClientFactory;
    private readonly ToolRegistry? _additionalTools;
    private readonly Func<string, int?> _resolveContextWindow;
    private readonly int? _maxIterations;

    private readonly Session _session;
    private readonly List<ChatMessage> _messages;
    private readonly ReadOnlyCollection<ChatMessage> _messagesView;
    private readonly TurnCallbacks? _hostCallbacks;
    private readonly PermissionGrantStore _grantStore;
    private readonly ILogger _logger;
    private string? _activeModelId;

    // Metrics accumulation — tracked across all turns in this session.
    // Written to {sessionId}.metrics.json alongside the session JSONL on dispose.
    private readonly Stopwatch _sessionTimer = Stopwatch.StartNew();
    private int _turnCount;
    private long _totalInputTokens;
    private long _totalOutputTokens;
    private int _toolCallsTotal;
    private int _toolCallsErrored;
    private string? _fatalError;

    /// <summary>
    /// Creates a session with explicit dependencies instead of a UrHost reference.
    /// Each parameter represents a specific capability the session needs, keeping
    /// the dependency surface narrow and testable.
    /// </summary>
    internal UrSession(
        UrConfiguration configuration,
        SkillRegistry skills,
        BuiltInCommandRegistry builtInCommands,
        Workspace workspace,
        ILoggerFactory loggerFactory,
        SessionStore sessions,
        Func<string, IChatClient> chatClientFactory,
        Session session,
        List<ChatMessage> messages,
        bool isPersisted,
        string? activeModelId,
        TurnCallbacks? callbacks,
        string workspacePermissionsPath,
        string alwaysPermissionsPath,
        Func<string, int?>? resolveContextWindow = null,
        ToolRegistry? additionalTools = null,
        TodoStore? todos = null,
        int? maxIterations = null)
    {
        _configuration = configuration;
        _skills = skills;
        _builtInCommands = builtInCommands;
        _workspace = workspace;
        _loggerFactory = loggerFactory;
        _sessions = sessions;
        _chatClientFactory = chatClientFactory;
        _resolveContextWindow = resolveContextWindow ?? (_ => null);
        _additionalTools = additionalTools;
        _maxIterations = maxIterations;
        _session = session;
        _messages = messages;
        _logger = loggerFactory.CreateLogger<UrSession>();
        Todos = todos ?? new TodoStore();
        // Expose a read-only view so callers (TUI, CLI) can render the message
        // list without being able to mutate it — mutation must go through
        // RunTurnAsync so persistence stays in sync.
        _messagesView = _messages.AsReadOnly();
        IsPersisted = isPersisted;
        _activeModelId = activeModelId;
        _hostCallbacks = callbacks;
        _grantStore = new PermissionGrantStore(
            workspacePermissionsPath, alwaysPermissionsPath,
            loggerFactory.CreateLogger<PermissionGrantStore>());

        // Restore token usage from persisted messages so the UI can display
        // context fill immediately when resuming a session. The last assistant
        // message's UsageContent reflects the most recent context size.
        LastInputTokens = ExtractLastInputTokens(messages);
    }

    public string Id => _session.Id;
    public DateTimeOffset CreatedAt => _session.CreatedAt;

    /// <summary>
    /// Whether this session has been written to disk at least once.
    /// A new session becomes persisted after the first user message is appended.
    /// </summary>
    public bool IsPersisted { get; private set; }

    public IReadOnlyList<ChatMessage> Messages => _messagesView;

    /// <summary>
    /// Session-scoped todo store. The LLM writes to it via <c>todo_write</c>;
    /// callers can observe it if they need structured task state outside the
    /// raw tool-call stream. It can be injected externally or constructed
    /// fresh in the constructor.
    /// </summary>
    public TodoStore Todos { get; }

    /// <summary>
    /// The model used for the current (or most recent) turn. Falls back to the
    /// host-level default if no turn has been run yet. The model is captured
    /// per-turn so that a mid-session model switch takes effect on the next turn.
    /// </summary>
    public string? ActiveModelId => _activeModelId ?? _configuration.SelectedModelId;

    /// <summary>
    /// The input token count from the most recent LLM call. Represents how much
    /// of the model's context window is filled. Initialized from persisted messages
    /// on session load and updated after each turn completes.
    /// </summary>
    public long? LastInputTokens { get; private set; }

    /// <summary>
    /// Runs a single conversational turn: appends the user message, drives the
    /// agent loop (LLM → tool calls → repeat), and streams events back to the
    /// caller for rendering. Messages are flushed to disk after each agent loop
    /// iteration so that a crash mid-turn preserves as much work as possible.
    /// </summary>
    /// <param name="userInput">The raw text from the user.</param>
    /// <param name="ct">Cancellation token — typically Ctrl+C from the UI.</param>
    public async IAsyncEnumerable<AgentLoop.AgentLoopEvent> RunTurnAsync(
        string userInput,
        [EnumeratorCancellation] CancellationToken ct = default)
    {
        try
        {
        // Gate: refuse to run if the system isn't fully configured (missing API
        // key or model selection). Emit a fatal TurnError rather than throwing so
        // the headless path (which only sees events) gets a clean exit code 1
        // instead of an unhandled exception crash. The TUI checks Readiness before
        // submitting a turn so this branch is primarily a safety net for headless mode.
        var readiness = _configuration.Readiness;
        if (!readiness.CanRunTurns)
        {
            yield return new AgentLoop.TurnError
            {
                Message = _configuration.GetProviderBlockingMessage(),
                IsFatal = true
            };
            yield break;
        }

        // Slash command interception: if the input starts with "/", treat it as
        // either a built-in command or a skill invocation. Built-ins are handled
        // here without reaching the LLM (execution is follow-up work). Skills
        // expand their template and replace the user input with tagged content.
        var effectiveInput = userInput;
        if (userInput.StartsWith('/') && userInput.Length > 1)
        {
            if (!TryExpandSlashCommand(userInput, out var expanded))
            {
                // Unknown command — not a built-in and not a user-invocable skill.
                yield return new AgentLoop.TurnError
                {
                    Message = $"Unknown command: {SlashCommandParser.ParseName(userInput)}",
                    IsFatal = false
                };
                yield break;
            }

            // expanded == null means a built-in was intercepted; stop the turn here.
            if (expanded is null)
                yield break;

            effectiveInput = expanded;
        }

        // Snapshot the model ID at turn start so that a concurrent config change
        // doesn't swap the model mid-conversation.
        _activeModelId = _configuration.SelectedModelId;
        _logger.LogInformation("Turn started: session={SessionId}, model={ModelId}", Id, _activeModelId);

        // Create one chat client for the entire turn and share it between
        // compaction and the agent loop. IChatClient wraps an HttpClient-backed
        // SDK object that holds network resources, so it must be disposed when
        // the turn is done. A single instance per turn is sufficient because
        // compaction runs before the agent loop starts.
        using var chatClient = _chatClientFactory(_activeModelId!);

        // --- Pre-turn compaction check ---
        // If the context window is filling up, summarize older messages before
        // appending the new user message. This runs proactively so we never
        // hit context-too-long API errors. Requires both a known context window
        // and prior token usage data (LastInputTokens from the previous turn).
        var contextWindow = _resolveContextWindow(_activeModelId!);
        if (contextWindow is null)
        {
            _logger.LogDebug(
                "Compaction skipped: context window unknown for model '{ModelId}'",
                _activeModelId);
        }

        if (contextWindow is not null && LastInputTokens is not null)
        {
            var compacted = await Compaction.Autocompactor.TryCompactAsync(
                _messages, chatClient, contextWindow.Value, LastInputTokens.Value,
                _logger, ct);

            if (compacted)
            {
                // Persist the compacted state: write a boundary sentinel, then
                // append each message in the now-compacted list. The boundary
                // tells ReadAllAsync to ignore everything before it on reload.
                await _sessions.AppendCompactBoundaryAsync(_session, ct);
                foreach (var msg in _messages)
                    await _sessions.AppendAsync(_session, msg, ct);

                // Critical invariant: persistedCount must always equal the
                // number of messages written to disk. After compaction, the
                // entire _messages list has been written post-boundary.
                // The user message added below will be the next un-persisted message.

                // Reset token count — will be refreshed by the next LLM call.
                LastInputTokens = null;

                yield return new AgentLoop.Compacted
                {
                    Message = "Context compacted — older messages summarized."
                };
            }
        }

        // Optimistically add the user message to the in-memory list and persist
        // it. If the write fails, roll back the in-memory state so the list
        // stays consistent with what's on disk.
        var userMessage = new ChatMessage(ChatRole.User, effectiveInput);
        _messages.Add(userMessage);

        try
        {
            await _sessions.AppendAsync(_session, userMessage, ct);
            IsPersisted = true;
        }
        catch (Exception ex)
        {
            _logger.LogError(ex, "Failed to persist user message for session '{SessionId}'", Id);
            _messages.RemoveAt(_messages.Count - 1);
            throw;
        }

        // Build a wrapped TurnCallbacks that layers grant-store checking in front
        // of the host-provided callback. This keeps grant persistence in the session
        // layer where it belongs — AgentLoop only sees a simple approve/deny callback.
        var wrappedCallbacks = BuildWrappedCallbacks();

        // Build the transient per-turn system prompt by composing the baseline
        // agent contract with feature-specific sections such as the current skill
        // listing. Keeping that composition here makes the dependency direction
        // explicit instead of letting the skills module own global prompt policy.
        var skillPromptSection = SkillPromptSectionBuilder.Build(_skills);
        var systemPrompt = SystemPromptBuilder.Build(skillPromptSection);

        // Build the per-turn tool registry, then append SubagentTool — which can
        // only be wired here because it needs per-turn context (chat client,
        // callbacks, system prompt) not available at registry build time.
        var tools = BuildToolRegistry();

        // SubagentTool is registered after the base registry is built so the runner
        // can close over the fully-populated registry (the sub-agent inherits all
        // parent tools except run_subagent itself, which SubagentRunner excludes).
        var subagentRunner = new AgentLoop.SubagentRunner(
            chatClient, tools, _workspace, wrappedCallbacks, systemPrompt,
            _loggerFactory);
        var subagentTool = new SubagentTool(subagentRunner);
        if (tools.Get(subagentTool.Name) is null)
        {
            tools.Register(
                subagentTool,
                ((IToolMeta)subagentTool).OperationType,
                targetExtractor: ((IToolMeta)subagentTool).TargetExtractor);
        }

        // Track how many messages have been flushed to disk. The agent loop
        // appends to _messages as it runs, and we flush after each iteration
        // so that tool call results survive a crash.
        var persistedCount = _messages.Count;
        var agentLoop = new AgentLoop.AgentLoop(
            chatClient, tools, _workspace,
            _loggerFactory.CreateLogger<AgentLoop.AgentLoop>(),
            _loggerFactory,
            _configuration.TurnsToKeepToolResults,
            _maxIterations);

        await foreach (var loopEvent in agentLoop.RunTurnAsync(_messages, wrappedCallbacks, systemPrompt, ct))
        {
            persistedCount = await PersistPendingMessagesAsync(persistedCount, ct);

            // Accumulate session-level metrics from events produced by the agent loop.
            // These counters feed the metrics JSON written on session close.
            switch (loopEvent)
            {
                case AgentLoop.ToolCallStarted:
                    _toolCallsTotal++;
                    break;
                case AgentLoop.ToolCallCompleted { IsError: true }:
                    _toolCallsErrored++;
                    break;
                case AgentLoop.TurnCompleted tc:
                    _turnCount++;
                    _totalInputTokens += tc.InputTokens ?? 0;
                    _totalOutputTokens += tc.OutputTokens ?? 0;
                    // Update the cached context fill level so callers (e.g. session resume)
                    // can read it without re-scanning messages.
                    LastInputTokens = tc.InputTokens;
                    break;
                case AgentLoop.TurnError { IsFatal: true } err:
                    _fatalError ??= err.Message;
                    break;
            }

            yield return loopEvent;
        }

        // Final flush for any messages produced after the last yield.
        await PersistPendingMessagesAsync(persistedCount, ct);
        _logger.LogInformation("Turn completed: session={SessionId}", Id);
        }
        finally
        {
            // Once the turn is finished, callers should see the latest selected
            // configuration model rather than a stale snapshot from an earlier turn.
            _activeModelId = null;
        }
    }

    /// <summary>
    /// Builds a tool registry using the unified factory pattern. Registers all
    /// built-in tools, the skill tool, and any test-injected additional tools.
    ///
    /// SubagentTool is NOT registered here — it needs per-turn context (chat
    /// client, callbacks, system prompt) that's only available in RunTurnAsync.
    /// RunTurnAsync appends it after calling this method.
    /// </summary>
    internal ToolRegistry BuildToolRegistry()
    {
        var registry = new ToolRegistry();
        var context = new ToolContext(_workspace, _session.Id, Todos);

        // Register builtins via the shared factory list.
        foreach (var (factory, operationType, targetExtractor) in BuiltinToolFactories.All)
        {
            var tool = factory(context);
            if (registry.Get(tool.Name) is null)
                registry.Register(tool, operationType, targetExtractor: targetExtractor);
        }

        // Skill tool, bound to the session ID for variable substitution.
        var skillTool = new SkillTool(_skills, context.SessionId);
        if (registry.Get(skillTool.Name) is null)
        {
            registry.Register(
                skillTool,
                ((IToolMeta)skillTool).OperationType,
                targetExtractor: ((IToolMeta)skillTool).TargetExtractor);
        }

        // Test-injected overrides (last-write-wins).
        _additionalTools?.MergeInto(registry);

        return registry;
    }

    /// <summary>
    /// Executes a recognized built-in slash command from outside the agent loop
    /// (i.e. from the TUI layer, before a turn is started).
    ///
    /// Returns a <see cref="Skills.CommandResult"/> when the command is recognized,
    /// or <c>null</c> when the name is not a registered built-in. The caller
    /// (OxApp) is responsible for ensuring that only valid arguments reach this
    /// method — for example, by blocking Enter until the model argument is known.
    ///
    /// This is the correct layer for command execution: UrSession holds
    /// _configuration (for persistence) and _builtInCommands (for dispatch).
    /// OxApp should dispatch and display only — not decide what constitutes a
    /// valid model ID or call SetSelectedModel directly.
    /// </summary>
    public Skills.CommandResult? ExecuteBuiltInCommand(string commandName, string? args)
    {
        if (_builtInCommands.Get(commandName) is null)
            return null;

        return commandName.ToLowerInvariant() switch
        {
            "model" => ExecuteModelCommand(args!),
            // /clear and /set remain stubs for now — execution moves here
            // but the actual behavior is deferred, same as before.
            _ => new Skills.CommandResult($"/{commandName} is not yet implemented.", IsError: true),
        };
    }

    /// <summary>
    /// Switches the active model (persisted to the user settings file).
    ///
    /// The argument is guaranteed non-null and valid by the time this is called
    /// — the TUI validates the model ID against ListAllModelIds() before allowing
    /// Enter to submit. This method is the happy-path only.
    ///
    /// SetSelectedModel is synchronous (local JSON file write + config reload),
    /// which matches the synchronous HandleKey path that calls this method.
    /// </summary>
    private Skills.CommandResult ExecuteModelCommand(string args)
    {
        _configuration.SetSelectedModel(args);
        return new Skills.CommandResult($"Model set to {args}.");
    }

    /// <summary>
    /// Attempts to handle a slash command. Returns true if the command was
    /// recognized (either as a built-in or a user-invocable skill), false if
    /// unknown. When true, <paramref name="expansion"/> is:
    ///   - null   → a built-in command was intercepted; the turn should end here
    ///              without sending anything to the LLM (execution is follow-up work).
    ///   - non-null → a skill was expanded; use the value as the effective LLM input.
    ///
    /// Built-in commands are checked first so they always take priority over any
    /// skill with the same name. Delegates parsing and formatting to
    /// <see cref="SlashCommandParser"/>.
    /// </summary>
    private bool TryExpandSlashCommand(string input, out string? expansion)
    {
        var commandName = SlashCommandParser.ParseName(input);

        // Built-in interception: the guard remains as a safety net for the
        // RunTurnAsync path. ExecuteBuiltInCommand (called before starting a
        // turn) handles all built-in execution; this branch should never be
        // reached in normal operation but protects against direct RunTurnAsync
        // calls with built-in command text.
        if (_builtInCommands.Get(commandName) is not null)
        {
            _logger.LogInformation("Built-in command /{Name} invoked (not yet implemented)", commandName);
            expansion = null;
            return true;
        }

        var args = SlashCommandParser.ParseArgs(input);
        var skill = _skills.Get(commandName);
        if (skill is null || !skill.UserInvocable)
        {
            expansion = null;
            return false;
        }

        var expanded = SkillExpander.Expand(skill, args, _session.Id);
        expansion = SlashCommandParser.FormatExpansion(commandName, args, expanded);
        return true;
    }

    /// <summary>
    /// Builds a TurnCallbacks that wraps the host-provided callback with grant-store
    /// checking. The wrapped callback is always non-null so that:
    ///   1. The grant store is consulted on every request (regardless of host callback).
    ///   2. Auto-deny happens here (not in AgentLoop) so grant checking always runs first.
    ///
    /// Decision flow:
    ///   1. Grant store already covers the request → approve immediately, no prompt.
    ///   2. No host callback configured → deny without prompting.
    ///   3. Host callback present → delegate; on durable grant, persist to grant store.
    /// </summary>
    private TurnCallbacks BuildWrappedCallbacks()
    {
        return new TurnCallbacks
        {
            // Pass SubagentEventEmitted through unchanged — the session layer has no
            // reason to intercept these; they belong entirely to the UI layer.
            SubagentEventEmitted = _hostCallbacks?.SubagentEventEmitted,

            RequestPermissionAsync = async (request, innerCt) =>
            {
                // Grant store covers it — no need to bother the user.
                if (_grantStore.IsCovered(request))
                    return new PermissionResponse(true, Scope: null);

                // No host callback: auto-deny.
                if (_hostCallbacks?.RequestPermissionAsync is null)
                    return new PermissionResponse(false, Scope: null);

                // Ask the host (CLI prompt, GUI dialog, etc).
                var response = await _hostCallbacks.RequestPermissionAsync(request, innerCt)
                    .ConfigureAwait(false);

                // Persist durable grants so the user isn't re-asked next turn (or next session).
                // Use innerCt here — this I/O is part of the callback's own async operation.
                if (response is not { Granted: true, Scope: not null and not PermissionScope.Once })
                    return response;

                var grant = new PermissionGrant(
                    request.OperationType,
                    request.Target,
                    response.Scope.Value,
                    request.ToolName);

                await _grantStore.StoreAsync(grant, innerCt).ConfigureAwait(false);

                return response;
            }
        };
    }

    /// <summary>
    /// Writes all messages that haven't been persisted yet to the session's
    /// JSONL file. Returns the new persisted count so the caller can track
    /// progress across multiple calls.
    ///
    /// On failure, un-persisted messages are removed from the in-memory list
    /// to maintain the invariant that _messages[0..persistedCount] matches
    /// what's on disk. This is a deliberate trade-off: we lose the agent's
    /// work rather than risking a divergence between memory and disk.
    /// </summary>
    private async Task<int> PersistPendingMessagesAsync(int persistedCount, CancellationToken ct)
    {
        while (persistedCount < _messages.Count)
        {
            try
            {
                await _sessions.AppendAsync(_session, _messages[persistedCount], ct);
                IsPersisted = true;
                persistedCount++;
            }
            catch (Exception ex)
            {
                // Roll back un-persisted messages to keep memory consistent
                // with the on-disk JSONL file.
                _logger.LogError(ex, "Failed to flush pending messages for session '{SessionId}'", Id);
                _messages.RemoveRange(persistedCount, _messages.Count - persistedCount);
                throw;
            }
        }

        return persistedCount;
    }

    /// <summary>
    /// Scans the loaded message list for the last assistant message containing
    /// <see cref="UsageContent"/> and returns its <see cref="UsageDetails.InputTokenCount"/>.
    /// Returns null if no usage data was persisted (provider didn't report, or new session).
    /// </summary>
    private static long? ExtractLastInputTokens(List<ChatMessage> messages)
    {
        // Walk backwards — the most recent assistant message with usage data
        // reflects the current context fill level.
        for (var i = messages.Count - 1; i >= 0; i--)
        {
            if (messages[i].Role != ChatRole.Assistant)
                continue;

            var usage = messages[i].Contents.OfType<UsageContent>().LastOrDefault();
            if (usage?.Details.InputTokenCount is { } tokens)
                return tokens;
        }

        return null;
    }

    /// <summary>
    /// Writes accumulated session metrics to a JSON file alongside the session JSONL.
    /// Called on session close — both TUI (when the app exits) and headless (after
    /// all turns finish) paths should dispose the session to trigger this.
    ///
    /// Metrics are only written if the session was persisted (at least one turn ran).
    /// If the write fails, the error is logged but not re-thrown — the session is
    /// already ending and losing metrics is acceptable.
    /// </summary>
    public async ValueTask DisposeAsync()
    {
        if (!IsPersisted)
            return;

        _sessionTimer.Stop();
        var metrics = new SessionMetrics
        {
            Turns = _turnCount,
            InputTokens = _totalInputTokens,
            OutputTokens = _totalOutputTokens,
            ToolCallsTotal = _toolCallsTotal,
            ToolCallsErrored = _toolCallsErrored,
            DurationSeconds = Math.Round(_sessionTimer.Elapsed.TotalSeconds, 1),
            Error = _fatalError,
        };

        try
        {
            await _sessions.WriteMetricsAsync(_session, metrics);
        }
        catch (Exception ex)
        {
            _logger.LogError(ex, "Failed to write metrics for session '{SessionId}'", Id);
        }
    }
}
