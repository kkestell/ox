using System.Text;
using CommunityToolkit.Mvvm.ComponentModel;

namespace Ur.Gui.ViewModels;

public abstract class MessageViewModel : ObservableObject
{
    public DateTimeOffset Timestamp { get; } = DateTimeOffset.Now;
}

public sealed class UserMessageViewModel : MessageViewModel
{
    public string Text { get; }

    public UserMessageViewModel(string text)
    {
        Text = text;
    }
}

public sealed partial class AssistantMessageViewModel : MessageViewModel
{
    private readonly StringBuilder _sb = new();

    [ObservableProperty]
    private string _text = string.Empty;

    [ObservableProperty]
    private bool _isStreaming = true;

    public void Append(string chunk)
    {
        _sb.Append(chunk);
        Text = _sb.ToString();
    }
}

public sealed partial class ToolMessageViewModel : MessageViewModel
{
    public string CallId { get; }
    public string ToolName { get; }

    [ObservableProperty]
    private string? _result;

    [ObservableProperty]
    private bool _isError;

    [ObservableProperty]
    private bool _isComplete;

    public ToolMessageViewModel(string callId, string toolName)
    {
        CallId = callId;
        ToolName = toolName;
    }
}

public sealed class SystemMessageViewModel : MessageViewModel
{
    public string Text { get; }
    public bool IsError { get; }

    public SystemMessageViewModel(string text, bool isError = false)
    {
        Text = text;
        IsError = isError;
    }
}
