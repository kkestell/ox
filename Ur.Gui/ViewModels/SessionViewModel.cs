using System.ClientModel;
using System.Collections.ObjectModel;
using Avalonia.Threading;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Ur;
using Ur.AgentLoop;

namespace Ur.Gui.ViewModels;

public sealed partial class SessionViewModel : ObservableObject
{
    private readonly UrSession _session;
    private CancellationTokenSource? _turnCts;

    public ObservableCollection<MessageViewModel> Messages { get; } = [];

    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(SendCommand))]
    private string _inputText = string.Empty;

    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(SendCommand))]
    private bool _isTurnRunning;

    public SessionViewModel(UrSession session)
    {
        _session = session;
    }

    private bool CanSend => !IsTurnRunning && !string.IsNullOrWhiteSpace(InputText);

    [RelayCommand(CanExecute = nameof(CanSend))]
    private async Task SendAsync()
    {
        var input = InputText.Trim();
        InputText = string.Empty;

        Messages.Add(new UserMessageViewModel(input));

        var streamingMsg = new AssistantMessageViewModel();
        Messages.Add(streamingMsg);

        IsTurnRunning = true;
        _turnCts = new CancellationTokenSource();

        try
        {
            await foreach (var evt in _session.RunTurnAsync(input, ct: _turnCts.Token))
            {
                var captured = evt;
                Dispatcher.UIThread.Post(() => Project(captured, streamingMsg));
            }
        }
        catch (OperationCanceledException)
        {
            // Turn was cancelled — finalize the message as-is.
        }
        catch (ClientResultException ex)
        {
            Dispatcher.UIThread.Post(() =>
                Messages.Add(new SystemMessageViewModel($"{ex.Message}\n\n{ex.GetRawResponse()?.Content}", isError: true)));
        }
        catch (HttpRequestException ex)
        {
            Dispatcher.UIThread.Post(() =>
                Messages.Add(new SystemMessageViewModel(ex.ToString(), isError: true)));
        }
        finally
        {
            Dispatcher.UIThread.Post(() =>
            {
                streamingMsg.IsStreaming = false;
                IsTurnRunning = false;
            });
            _turnCts.Dispose();
            _turnCts = null;
        }
    }

    public void CancelTurn() => _turnCts?.Cancel();

    private void Project(AgentLoopEvent evt, AssistantMessageViewModel streamingMsg)
    {
        switch (evt)
        {
            case ResponseChunk chunk:
                streamingMsg.Append(chunk.Text);
                break;

            case ToolCallStarted started:
                Messages.Add(new ToolMessageViewModel(started.CallId, started.ToolName));
                break;

            case ToolCallCompleted completed:
                var toolMsg = Messages.OfType<ToolMessageViewModel>()
                    .FirstOrDefault(m => m.CallId == completed.CallId);
                if (toolMsg is not null)
                {
                    toolMsg.Result = completed.Result;
                    toolMsg.IsError = completed.IsError;
                    toolMsg.IsComplete = true;
                }
                break;

            case TurnCompleted:
                streamingMsg.IsStreaming = false;
                IsTurnRunning = false;
                break;

            case Error error:
                Messages.Add(new SystemMessageViewModel(error.Message, isError: true));
                if (error.IsFatal)
                    IsTurnRunning = false;
                break;
        }
    }
}
