using System.Threading.Channels;

namespace Ox;

/// <summary>
/// Coordinates the chat composer workflow between the InputAreaView widget and
/// the background REPL turn loop.
///
/// Ownership boundary:
///   - InputAreaView is a widget: it emits raw user intents via OnViewSubmit
///     and OnViewEof.
///   - ComposerController is the interpreter: it routes submissions to the
///     chat channel.
///   - The REPL loop is the consumer: it awaits ChatSubmissions.ReadAsync and
///     never touches the view's async plumbing directly.
///
/// Permission prompts are handled entirely by PermissionPromptView — the
/// controller is no longer involved in that flow.
///
/// Chat submissions flow through an unbounded Channel so typed-ahead input is
/// never dropped — the REPL loop drains them as turns complete.
/// </summary>
internal sealed class ComposerController
{
    // Unbounded so typed-ahead submissions accumulate without backpressure.
    // SingleReader because only the REPL loop reads; AllowSynchronousContinuations
    // disabled to keep continuations off the UI thread that calls TryWrite.
    private readonly Channel<string> _chatChannel =
        Channel.CreateUnbounded<string>(new UnboundedChannelOptions
        {
            SingleReader = true,
            AllowSynchronousContinuations = false
        });

    /// <summary>
    /// The chat submission queue. The REPL loop awaits ReadAsync on this reader
    /// instead of arming a TaskCompletionSource per turn.
    /// </summary>
    public ChannelReader<string> ChatSubmissions => _chatChannel.Reader;

    /// <summary>
    /// Called from the UI thread (view) when the user presses Enter.
    /// Pushes the text into the unbounded channel, preserving typed-ahead input
    /// even when no turn is actively waiting to consume it.
    /// </summary>
    public void OnViewSubmit(string text)
    {
        _chatChannel.Writer.TryWrite(text);
    }

    /// <summary>
    /// Called from the UI thread when the user signals EOF (Ctrl+C, Ctrl+D).
    /// Completes the channel writer so ReadAsync on the REPL thread returns
    /// immediately and the loop exits cleanly.
    /// </summary>
    public void OnViewEof()
    {
        _chatChannel.Writer.TryComplete();
    }
}
