using System.Threading.Channels;

namespace Ox;

/// <summary>
/// The two states the composer can be in at any given moment.
/// Chat is the default; Permission is entered for the duration of a single
/// tool-permission prompt and then immediately restored to Chat.
/// </summary>
internal enum ComposerMode
{
    Chat,
    Permission
}

/// <summary>
/// Coordinates the composer workflow between the InputAreaView widget, the
/// background REPL turn loop, and the PermissionHandler.
///
/// Ownership boundary:
///   - InputAreaView is a widget: it emits raw user intents via OnViewSubmit
///     and OnViewEof, and reads Mode for key-handling guards.
///   - ComposerController is the interpreter: it routes submissions to the
///     chat channel or the pending permission session based on the current mode.
///   - The REPL loop is the consumer: it awaits ChatSubmissions.ReadAsync and
///     never touches the view's async plumbing directly.
///   - PermissionHandler is the other consumer: it calls EnterPermissionMode on
///     the UI thread (via app.Invoke) and awaits the returned Task from the
///     background thread.
///
/// Chat submissions flow through an unbounded Channel so typed-ahead input is
/// never dropped — the REPL loop drains them as turns complete.
/// Permission prompts use a single TaskCompletionSource slot, resolving exactly
/// once per prompt and never disturbing the chat queue.
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

    // Non-null only while Mode == Permission.
    // Resolved by OnViewSubmit/OnViewEof; cancelled by the permission CancellationToken.
    private TaskCompletionSource<string?>? _pendingPermission;

    /// <summary>
    /// Current composer mode. Chat by default; Permission during a prompt.
    /// Read by InputAreaView to gate mode-specific key shortcuts.
    /// </summary>
    public ComposerMode Mode { get; private set; } = ComposerMode.Chat;

    /// <summary>
    /// The chat submission queue. The REPL loop awaits ReadAsync on this reader
    /// instead of arming a TaskCompletionSource per turn.
    /// </summary>
    public ChannelReader<string> ChatSubmissions => _chatChannel.Reader;

    /// <summary>
    /// Called from the UI thread (view) when the user presses Enter.
    ///
    /// In Chat mode the text is pushed into the unbounded channel, preserving
    /// typed-ahead input even when no turn is actively waiting to consume it.
    /// In Permission mode it resolves the pending permission TCS so the
    /// background awaiter unblocks with the user's answer.
    /// </summary>
    public void OnViewSubmit(string text)
    {
        if (Mode == ComposerMode.Chat)
            _chatChannel.Writer.TryWrite(text);
        else
            _pendingPermission?.TrySetResult(text);
    }

    /// <summary>
    /// Called from the UI thread when the user signals EOF (Ctrl+C, Ctrl+D).
    ///
    /// In Chat mode, completes the channel writer so ReadAsync on the REPL
    /// thread returns immediately and the loop exits cleanly.
    /// In Permission mode, resolves the pending TCS with null to deny the
    /// request — the chat channel is deliberately left open so the loop can
    /// continue if the user later resumes interaction.
    /// </summary>
    public void OnViewEof()
    {
        if (Mode == ComposerMode.Chat)
            _chatChannel.Writer.TryComplete();
        else
            _pendingPermission?.TrySetResult(null);
    }

    /// <summary>
    /// Switches the controller into Permission mode and returns a Task that
    /// resolves with the user's raw response (or null if cancelled/denied).
    ///
    /// MUST be called on the UI thread (via app.Invoke) so the mode switch
    /// and the view's prompt update happen atomically before any keystroke
    /// arrives. The caller awaits the returned Task from the background thread.
    ///
    /// Cancellation via <paramref name="ct"/> resolves the task with null,
    /// denying the permission request without terminating the chat channel.
    /// </summary>
    public Task<string?> EnterPermissionMode(CancellationToken ct = default)
    {
        Mode = ComposerMode.Permission;
        _pendingPermission = new TaskCompletionSource<string?>(
            TaskCreationOptions.RunContinuationsAsynchronously);

        // Link cancellation: a cancelled CT denies permission without affecting
        // the chat queue or the overall REPL loop.
        if (ct.CanBeCanceled)
            ct.Register(() => _pendingPermission.TrySetResult(null));

        return _pendingPermission.Task;
    }

    /// <summary>
    /// Restores Chat mode after a permission session completes.
    /// MUST be called on the UI thread after the permission task has resolved.
    /// </summary>
    public void ExitPermissionMode()
    {
        _pendingPermission = null;
        Mode = ComposerMode.Chat;
    }
}
