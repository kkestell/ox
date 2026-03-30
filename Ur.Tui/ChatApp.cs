using System.Collections.Concurrent;
using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Terminal.Rendering;
using Ur.Tui.Components;
using Ur.Tui.Dummy;
using Ur.Tui.State;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui;

public sealed class ChatApp
{
    private readonly DummyConfiguration _config;
    private readonly Layer _baseLayer;
    private readonly Layer _overlayLayer;
    private readonly Compositor _compositor;
    private readonly ChatState _state = new();
    private readonly MessageList _messageList;
    private readonly ChatInput _chatInput = new();
    private readonly Dictionary<string, Action<string>> _slashCommands;
    private readonly ConcurrentQueue<DummyAgentLoopEvent> _eventQueue = new();

    private DummySession? _session;
    private CancellationTokenSource? _turnCts;
    private bool _exitRequested;
    private bool _isFirstRun = true;

    public ChatApp(DummyConfiguration config, Compositor compositor, Layer baseLayer, Layer overlayLayer)
    {
        _config = config;
        _compositor = compositor;
        _baseLayer = baseLayer;
        _overlayLayer = overlayLayer;
        _messageList = new MessageList(_state);

        _slashCommands = new Dictionary<string, Action<string>>(StringComparer.OrdinalIgnoreCase)
        {
            ["quit"] = _ => _exitRequested = true,
            ["model"] = _ => OpenModelPicker(),
        };

        // Check readiness on startup
        CheckReadiness();
    }

    // Exposed for testing
    internal ChatState State => _state;
    internal ChatInput ChatInput => _chatInput;

    public bool ProcessFrame(ReadOnlySpan<KeyEvent> keys)
    {
        var w = _compositor.Width;
        var h = _compositor.Height;

        // Resize layers if needed
        if (_baseLayer.Width != w || _baseLayer.Height != h)
            _baseLayer.Resize(w, h);
        if (_overlayLayer.Width != w || _overlayLayer.Height != h)
            _overlayLayer.Resize(w, h);

        // Drain agent events
        DrainAgentEvents();

        // Process key events
        foreach (var key in keys)
        {
            if (!ProcessKey(key))
                return false;
        }

        if (_exitRequested)
            return false;

        // Render
        RenderFrame(w, h);

        return true;
    }

    private bool ProcessKey(KeyEvent key)
    {
        if (key.EventType == KeyEventType.Release)
            return true;

        // Ctrl+C: cancel turn if running, exit if idle
        if (key is { Key: Key.C, Mods: Modifiers.Ctrl })
        {
            if (_state.IsTurnRunning)
            {
                CancelTurn();
                return true;
            }
            return false; // Exit
        }

        // Modal routing
        if (_state.ActiveModal is not null)
        {
            var consumed = _state.ActiveModal.HandleKey(key);
            if (!consumed)
                return HandleModalResult();
            return true;
        }

        // Scroll keys go to message list regardless
        if (key.Key is Key.PageUp or Key.PageDown)
        {
            _messageList.HandleKey(key);
            return true;
        }

        // Chat input
        var inputConsumed = _chatInput.HandleKey(key);
        if (!inputConsumed) // Enter was pressed
        {
            var text = _chatInput.Text.Trim();
            _chatInput.Clear();

            if (text.Length == 0)
                return true;

            if (text.StartsWith('/'))
                return DispatchSlashCommand(text);

            SubmitMessage(text);
        }

        return true;
    }

    private bool HandleModalResult()
    {
        switch (_state.ActiveModal)
        {
            case ApiKeyModal apiKey:
                if (apiKey.Submitted && !string.IsNullOrWhiteSpace(apiKey.Value))
                {
                    _config.SetApiKey(apiKey.Value);
                    _state.ActiveModal = null;
                    CheckReadiness();
                    return true;
                }
                if (apiKey.Dismissed)
                {
                    if (_isFirstRun)
                        return false; // Exit on first-run dismiss
                    _state.ActiveModal = null;
                    return true;
                }
                break;

            case ModelPickerModal picker:
                if (picker.Submitted && picker.SelectedModel is not null)
                {
                    _config.SetSelectedModel(picker.SelectedModel.Id);
                    _state.ActiveModal = null;
                    if (_isFirstRun)
                    {
                        _isFirstRun = false;
                        _session = new DummySession();
                        AddSystemMessage("Ready. Type a message or /help for commands.");
                    }
                    return true;
                }
                if (picker.Dismissed)
                {
                    if (_isFirstRun)
                        return false; // Exit on first-run dismiss
                    _state.ActiveModal = null;
                    return true;
                }
                break;
        }

        _state.ActiveModal = null;
        return true;
    }

    private void CheckReadiness()
    {
        var readiness = _config.Readiness;
        if (readiness.BlockingIssues.Contains(DummyBlockingIssue.MissingApiKey))
        {
            _state.ActiveModal = new ApiKeyModal();
            return;
        }
        if (readiness.BlockingIssues.Contains(DummyBlockingIssue.MissingModelSelection))
        {
            _state.ActiveModal = new ModelPickerModal(_config.AvailableModels);
            return;
        }

        // All clear
        if (_isFirstRun)
        {
            _isFirstRun = false;
            _session = new DummySession();
            AddSystemMessage("Ready. Type a message or /help for commands.");
        }
    }

    private void OpenModelPicker()
    {
        _state.ActiveModal = new ModelPickerModal(_config.AvailableModels);
    }

    private bool DispatchSlashCommand(string text)
    {
        var spaceIndex = text.IndexOf(' ', 1);
        var commandName = spaceIndex < 0 ? text[1..] : text[1..spaceIndex];
        var args = spaceIndex < 0 ? "" : text[(spaceIndex + 1)..];

        if (_slashCommands.TryGetValue(commandName, out var handler))
        {
            handler(args);
            return !_exitRequested;
        }

        AddSystemMessage($"Unknown command: /{commandName}");
        return true;
    }

    private void SubmitMessage(string text)
    {
        if (_session is null || _state.IsTurnRunning)
            return;

        // Add user message
        var userMsg = new DisplayMessage(MessageRole.User);
        userMsg.Content.Append(text);
        _state.Messages.Add(userMsg);

        // Add streaming assistant message
        var assistantMsg = new DisplayMessage(MessageRole.Assistant) { IsStreaming = true };
        _state.Messages.Add(assistantMsg);

        // Start turn
        _state.IsTurnRunning = true;
        _state.ScrollOffset = 0;
        _turnCts = new CancellationTokenSource();
        var ct = _turnCts.Token;

        _ = Task.Run(async () =>
        {
            try
            {
                await foreach (var evt in _session.RunTurnAsync(text, ct))
                    _eventQueue.Enqueue(evt);
            }
            catch (OperationCanceledException)
            {
                // Cancelled via Ctrl+C
            }
            catch (Exception ex)
            {
                _eventQueue.Enqueue(new DummyError { Message = ex.Message, IsFatal = false });
            }
            finally
            {
                _eventQueue.Enqueue(new DummyTurnCompleted());
            }
        });
    }

    private void DrainAgentEvents()
    {
        while (_eventQueue.TryDequeue(out var evt))
        {
            switch (evt)
            {
                case DummyResponseChunk chunk:
                    var streamingMsg = GetStreamingMessage();
                    streamingMsg?.Content.Append(chunk.Text);
                    break;

                case DummyToolCallStarted tool:
                    var toolMsg = new DisplayMessage(MessageRole.Tool) { ToolName = tool.ToolName };
                    toolMsg.Content.Append($"Running {tool.ToolName}...");
                    // Insert before the streaming assistant message
                    var insertIdx = _state.Messages.Count - 1;
                    if (insertIdx >= 0)
                        _state.Messages.Insert(insertIdx, toolMsg);
                    else
                        _state.Messages.Add(toolMsg);
                    break;

                case DummyToolCallCompleted toolDone:
                    // Find and update the tool message
                    for (var i = _state.Messages.Count - 1; i >= 0; i--)
                    {
                        if (_state.Messages[i] is { Role: MessageRole.Tool, ToolName: var tn } && tn == toolDone.ToolName)
                        {
                            _state.Messages[i].Content.Clear();
                            _state.Messages[i].Content.Append(toolDone.Result);
                            break;
                        }
                    }
                    break;

                case DummyTurnCompleted:
                    var sm = GetStreamingMessage();
                    if (sm is not null)
                        sm.IsStreaming = false;
                    _state.IsTurnRunning = false;
                    _turnCts?.Dispose();
                    _turnCts = null;
                    break;

                case DummyError error:
                    var errMsg = new DisplayMessage(MessageRole.System) { IsError = true };
                    errMsg.Content.Append(error.Message);
                    _state.Messages.Add(errMsg);
                    break;
            }
        }
    }

    private DisplayMessage? GetStreamingMessage()
    {
        for (var i = _state.Messages.Count - 1; i >= 0; i--)
        {
            if (_state.Messages[i] is { Role: MessageRole.Assistant, IsStreaming: true })
                return _state.Messages[i];
        }
        return null;
    }

    private void CancelTurn()
    {
        _turnCts?.Cancel();
    }

    private void AddSystemMessage(string text)
    {
        var msg = new DisplayMessage(MessageRole.System);
        msg.Content.Append(text);
        _state.Messages.Add(msg);
    }

    private void RenderFrame(int w, int h)
    {
        // Layout
        var inputHeight = 1;
        var messageHeight = h - inputHeight;
        var messageRect = new Rect(0, 0, w, messageHeight);
        var inputRect = new Rect(0, messageHeight, w, inputHeight);

        // Render base layer
        _baseLayer.Clear();
        _messageList.Render(_baseLayer.Content, messageRect);
        _chatInput.Render(_baseLayer.Content, inputRect);

        // Render overlay layer
        _overlayLayer.Clear();
        if (_state.ActiveModal is not null)
        {
            var screenRect = new Rect(0, 0, w, h);
            _state.ActiveModal.Render(_overlayLayer.Content, screenRect);

            // Shadow for the modal
            var (mw, mh) = _state.ActiveModal switch
            {
                ApiKeyModal => (ApiKeyModal.ModalWidth, ApiKeyModal.ModalHeight),
                ModelPickerModal => (ModelPickerModal.ModalWidth, ModelPickerModal.ModalHeight),
                _ => (40, 10),
            };
            var mx = (w - mw) / 2;
            var my = (h - mh) / 2;

            // L-shaped shadow: right strip + bottom strip
            _overlayLayer.MarkShadow(new Rect(mx + mw, my + 1, 2, mh));
            _overlayLayer.MarkShadow(new Rect(mx + 2, my + mh, mw, 1));
        }
    }
}
