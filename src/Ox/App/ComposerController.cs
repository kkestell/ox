using System.Threading.Channels;

namespace Ox.App;

/// <summary>
/// Bridges the input view and the REPL loop via a bounded channel.
///
/// When the user presses Enter, the view calls <see cref="OnViewSubmit"/> which
/// enqueues the text. The REPL loop reads from <see cref="ChatSubmissions"/>
/// to receive messages in FIFO order. This decouples the UI thread from the
/// turn-processing task and allows type-ahead (the user can submit messages
/// while the agent is still processing a previous turn).
///
/// Permission prompts are handled separately by PermissionPromptView and do
/// not flow through this controller.
/// </summary>
public sealed class ComposerController
{
    private readonly Channel<string> _chatChannel = Channel.CreateUnbounded<string>();

    /// <summary>
    /// Read side of the chat submission channel. The REPL loop awaits this
    /// to receive user messages.
    /// </summary>
    public ChannelReader<string> ChatSubmissions => _chatChannel.Reader;

    /// <summary>
    /// Called by the input view when the user presses Enter. Enqueues the
    /// message text for the REPL loop to pick up.
    /// </summary>
    public void OnViewSubmit(string message)
    {
        _chatChannel.Writer.TryWrite(message);
    }

    /// <summary>
    /// Called when the user signals EOF (Ctrl+D with empty buffer). Completes
    /// the channel writer so the REPL loop exits cleanly.
    /// </summary>
    public void OnViewEof()
    {
        _chatChannel.Writer.Complete();
    }
}
