using System.Collections.ObjectModel;
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
/// a message. A session is created via <see cref="UrHost.CreateSession"/> (new)
/// or <see cref="UrHost.OpenSessionAsync"/> (resume from disk).
///
/// Architecture: The message list is mutable and shared with the agent loop.
/// As the loop produces assistant messages and tool results, they are appended
/// to <see cref="_messages"/> and periodically flushed to disk via
/// <see cref="PersistPendingMessagesAsync"/>. This "append and flush" approach
/// avoids writing the entire conversation on every token — only new messages
/// are written, and a crash loses at most the messages produced after the last
/// flush point.
/// </summary>
public sealed class UrSession
{
    private readonly UrHost _host;
    private readonly Session _session;
    private readonly List<ChatMessage> _messages;
    private readonly ReadOnlyCollection<ChatMessage> _messagesView;
    private readonly TurnCallbacks? _hostCallbacks;
    private readonly PermissionGrantStore _grantStore;
    private readonly ILogger _logger;
    private string? _activeModelId;

    internal UrSession(
        UrHost host,
        Session session,
        List<ChatMessage> messages,
        bool isPersisted,
        string? activeModelId,
        TurnCallbacks? callbacks,
        string workspacePermissionsPath,
        string alwaysPermissionsPath,
        TodoStore? todos = null)
    {
        _host = host;
        _session = session;
        _messages = messages;
        _logger = host.LoggerFactory.CreateLogger<UrSession>();
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
            host.LoggerFactory.CreateLogger<PermissionGrantStore>());

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
    /// The model used for the current (or most recent) turn. Falls back to the
    /// host-level default if no turn has been run yet. The model is captured
    /// per-turn so that a mid-session model switch takes effect on the next turn.
    /// </summary>
    /// <summary>
    /// Session-scoped todo store. The LLM writes to it via <c>todo_write</c>;
    /// callers can observe it if they need structured task state outside the
    /// raw tool-call stream. It can be injected externally or constructed
    /// fresh in the constructor.
    /// </summary>
    public TodoStore Todos { get; }

    public string? ActiveModelId => _activeModelId ?? _host.Configuration.SelectedModelId;

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
        // Gate: refuse to run if the system isn't fully configured (missing API
        // key or model selection). This check is intentionally performed here
        // rather than at session creation so that the caller gets a clear
        // exception with the specific blocking issues.
        var readiness = _host.Configuration.Readiness;
        if (!readiness.CanRunTurns)
            throw new ChatNotReadyException(readiness);

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
        _activeModelId = _host.Configuration.SelectedModelId;
        _logger.LogInformation("Turn started: session={SessionId}, model={ModelId}", Id, _activeModelId);

        // Optimistically add the user message to the in-memory list and persist
        // it. If the write fails, roll back the in-memory state so the list
        // stays consistent with what's on disk.
        var userMessage = new ChatMessage(ChatRole.User, effectiveInput);
        _messages.Add(userMessage);

        try
        {
            await _host.AppendMessageAsync(_session, userMessage, ct);
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
        var skillPromptSection = SkillPromptSectionBuilder.Build(_host.Skills);
        var systemPrompt = SystemPromptBuilder.Build(skillPromptSection);

        // Build the per-turn tool registry from the shared factory loop in UrHost,
        // then append SubagentTool — which can only be wired here because it needs
        // per-turn context (chat client, callbacks, system prompt) not available in
        // BuildSessionToolRegistry.
        var chatClient = _host.CreateChatClient(_activeModelId!);
        var tools = _host.BuildSessionToolRegistry(_session.Id, Todos);

        // SubagentTool is registered after the base registry is built so the runner
        // can close over the fully-populated registry (the sub-agent inherits all
        // parent tools except run_subagent itself, which SubagentRunner excludes).
        var subagentRunner = new AgentLoop.SubagentRunner(
            chatClient, tools, _host.Workspace, wrappedCallbacks, systemPrompt,
            _host.LoggerFactory);
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
            chatClient, tools, _host.Workspace,
            _host.LoggerFactory.CreateLogger<AgentLoop.AgentLoop>(),
            _host.LoggerFactory);

        await foreach (var loopEvent in agentLoop.RunTurnAsync(_messages, wrappedCallbacks, systemPrompt, ct))
        {
            persistedCount = await PersistPendingMessagesAsync(persistedCount, ct);

            // Update the cached context fill level so callers (e.g. session resume)
            // can read it without re-scanning messages.
            if (loopEvent is AgentLoop.TurnCompleted { InputTokens: { } tokens })
                LastInputTokens = tokens;

            yield return loopEvent;
        }

        // Final flush for any messages produced after the last yield.
        await PersistPendingMessagesAsync(persistedCount, ct);
        _logger.LogInformation("Turn completed: session={SessionId}", Id);
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

        // Built-in interception: log and swallow — actual execution is future work.
        if (_host.BuiltInCommands.Get(commandName) is not null)
        {
            _logger.LogInformation("Built-in command /{Name} invoked (not yet implemented)", commandName);
            expansion = null;
            return true;
        }

        var args = SlashCommandParser.ParseArgs(input);
        var skill = _host.Skills.Get(commandName);
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
                    request.RequestingExtension);

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
                await _host.AppendMessageAsync(_session, _messages[persistedCount], ct);
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
}
