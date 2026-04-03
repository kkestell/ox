using System.Collections.ObjectModel;
using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.Sessions;

namespace Ur;

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
        _messagesView = _messages.AsReadOnly();
        _isPersisted = isPersisted;
        _activeModelId = activeModelId;
    }

    public string Id => _session.Id;
    public DateTimeOffset CreatedAt => _session.CreatedAt;
    public bool IsPersisted => _isPersisted;
    public IReadOnlyList<ChatMessage> Messages => _messagesView;
    public string? ActiveModelId => _activeModelId ?? _host.Configuration.SelectedModelId;

    public async IAsyncEnumerable<AgentLoop.AgentLoopEvent> RunTurnAsync(
        string userInput,
        TurnCallbacks? turnCallbacks = null,
        [EnumeratorCancellation] CancellationToken ct = default)
    {
        var readiness = _host.Configuration.Readiness;
        if (!readiness.CanRunTurns)
            throw new ChatNotReadyException(readiness);

        _activeModelId = _host.Configuration.SelectedModelId;

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

        var persistedCount = _messages.Count;
        var agentLoop = new AgentLoop.AgentLoop(_host.CreateChatClient(_activeModelId!), _host.Tools);

        await foreach (var loopEvent in agentLoop.RunTurnAsync(_messages, ct))
        {
            persistedCount = await PersistPendingMessagesAsync(persistedCount, ct);
            yield return loopEvent;
        }

        await PersistPendingMessagesAsync(persistedCount, ct);
    }

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
                _messages.RemoveRange(persistedCount, _messages.Count - persistedCount);
                throw;
            }
        }

        return persistedCount;
    }
}
