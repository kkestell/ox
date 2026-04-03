using System.Collections.ObjectModel;
using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.Configuration;
using Ur.Permissions;

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
    private bool _isPersisted;
    private string? _activeModelId;

    internal UrSession(
        UrHost host,
        Session session,
        List<ChatMessage> messages,
        bool isPersisted,
        string? activeModelId)
    {
        _host = host;
        _session = session;
        _messages = messages;
        // Expose a read-only view so callers (TUI, CLI) can render the message
        // list without being able to mutate it — mutation must go through
        // RunTurnAsync so persistence stays in sync.
        _messagesView = _messages.AsReadOnly();
        _isPersisted = isPersisted;
        _activeModelId = activeModelId;
    }

    public string Id => _session.Id;
    public DateTimeOffset CreatedAt => _session.CreatedAt;

    /// <summary>
    /// Whether this session has been written to disk at least once.
    /// A new session becomes persisted after the first user message is appended.
    /// </summary>
    public bool IsPersisted => _isPersisted;

    public IReadOnlyList<ChatMessage> Messages => _messagesView;

    /// <summary>
    /// The model used for the current (or most recent) turn. Falls back to the
    /// host-level default if no turn has been run yet. The model is captured
    /// per-turn so that a mid-session model switch takes effect on the next turn.
    /// </summary>
    public string? ActiveModelId => _activeModelId ?? _host.Configuration.SelectedModelId;

    /// <summary>
    /// Runs a single conversational turn: appends the user message, drives the
    /// agent loop (LLM → tool calls → repeat), and streams events back to the
    /// caller for rendering. Messages are flushed to disk after each agent loop
    /// iteration so that a crash mid-turn preserves as much work as possible.
    /// </summary>
    /// <param name="userInput">The raw text from the user.</param>
    /// <param name="turnCallbacks">Optional callbacks for permission prompts (not yet wired).</param>
    /// <param name="ct">Cancellation token — typically Ctrl+C from the UI.</param>
    public async IAsyncEnumerable<AgentLoop.AgentLoopEvent> RunTurnAsync(
        string userInput,
        TurnCallbacks? turnCallbacks = null,
        [EnumeratorCancellation] CancellationToken ct = default)
    {
        // Gate: refuse to run if the system isn't fully configured (missing API
        // key or model selection). This check is intentionally performed here
        // rather than at session creation so that the caller gets a clear
        // exception with the specific blocking issues.
        var readiness = _host.Configuration.Readiness;
        if (!readiness.CanRunTurns)
            throw new ChatNotReadyException(readiness);

        // Snapshot the model ID at turn start so that a concurrent config change
        // doesn't swap the model mid-conversation.
        _activeModelId = _host.Configuration.SelectedModelId;

        // Optimistically add the user message to the in-memory list and persist
        // it. If the write fails, roll back the in-memory state so the list
        // stays consistent with what's on disk.
        var userMessage = new ChatMessage(ChatRole.User, userInput);
        _messages.Add(userMessage);

        try
        {
            await _host.AppendMessageAsync(_session, userMessage, ct);
            _isPersisted = true;
        }
        catch
        {
            _messages.RemoveAt(_messages.Count - 1);
            throw;
        }

        // Track how many messages have been flushed to disk. The agent loop
        // appends to _messages as it runs, and we flush after each iteration
        // so that tool call results survive a crash.
        var persistedCount = _messages.Count;
        var agentLoop = new AgentLoop.AgentLoop(_host.CreateChatClient(_activeModelId!), _host.Tools);

        await foreach (var loopEvent in agentLoop.RunTurnAsync(_messages, ct))
        {
            persistedCount = await PersistPendingMessagesAsync(persistedCount, ct);
            yield return loopEvent;
        }

        // Final flush for any messages produced after the last yield.
        await PersistPendingMessagesAsync(persistedCount, ct);
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
                _isPersisted = true;
                persistedCount++;
            }
            catch
            {
                // Roll back un-persisted messages to keep memory consistent
                // with the on-disk JSONL file.
                _messages.RemoveRange(persistedCount, _messages.Count - persistedCount);
                throw;
            }
        }

        return persistedCount;
    }
}
