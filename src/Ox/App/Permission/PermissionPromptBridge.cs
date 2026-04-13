using Ox.Agent.Permissions;
using Ox.Terminal.Input;
using Ox.Terminal.Rendering;

namespace Ox.App.Permission;

/// <summary>
/// Bridges the async <see cref="TurnCallbacks.RequestPermissionAsync"/> contract
/// to the single-threaded TUI main loop.
///
/// When the agent loop asks for permission, the background turn task awaits
/// a <see cref="TaskCompletionSource{TResult}"/> while the main loop shows the
/// <see cref="PermissionPromptView"/> and completes the TCS from the key handler.
/// Extracting this logic into a dedicated bridge isolates the cross-thread TCS
/// plumbing from the rest of <see cref="OxApp"/> so the coordinator doesn't
/// need to know about "there's a pending permission request" as a top-level state.
/// </summary>
internal sealed class PermissionPromptBridge(Action wakeMainLoop)
{
    /// <summary>The prompt view this bridge drives.</summary>
    public PermissionPromptView View { get; } = new();

    private TaskCompletionSource<PermissionResponse>? _tcs;

    /// <summary>Whether a permission prompt is currently visible.</summary>
    public bool IsActive => View.IsActive;

    /// <summary>
    /// Implements <see cref="TurnCallbacks.RequestPermissionAsync"/>. Called
    /// from the background turn task: sets up the view, wakes the main loop
    /// so it can render the prompt, and blocks on the TCS until the user
    /// responds (or cancellation fires, which auto-denies).
    /// </summary>
    public async ValueTask<PermissionResponse> RequestAsync(
        PermissionRequest request,
        CancellationToken ct)
    {
        _tcs = new TaskCompletionSource<PermissionResponse>();
        View.ActiveRequest = request;
        View.Editor.Clear();
        wakeMainLoop();

        using var reg = ct.Register(() =>
            _tcs.TrySetResult(new PermissionResponse(Granted: false, Scope: null)));

        return await _tcs.Task;
    }

    /// <summary>
    /// Routes a keypress while the prompt is visible. Escape / Ctrl+C / 'n'
    /// deny; Enter approves (or interprets a typed scope alias); typing edits
    /// the scope-input field. Always returns true to indicate the key was
    /// consumed — the prompt is modal while visible.
    /// </summary>
    public bool HandleKey(KeyEventArgs args)
    {
        var bare = args.KeyCode.WithoutModifiers();

        // Escape, Ctrl+C, or 'n' → deny.
        if (bare == KeyCode.Esc || (bare == KeyCode.C && args.KeyCode.HasCtrl()) || bare == KeyCode.N)
        {
            Resolve(new PermissionResponse(Granted: false, Scope: null));
            return true;
        }

        // Enter → approve (or submit typed scope).
        if (bare == KeyCode.Enter)
        {
            var input = View.Editor.Text.Trim().ToLowerInvariant();
            var request = View.ActiveRequest!;

            if (input is "n" or "no")
            {
                Resolve(new PermissionResponse(Granted: false, Scope: null));
                return true;
            }

            // Accept both the long scope names and the compact aliases shown in
            // the prompt so the shorter copy is actually actionable.
            PermissionScope? scope = input switch
            {
                "o" or "once" => PermissionScope.Once,
                "s" or "session" => PermissionScope.Session,
                "w" or "ws" or "workspace" => PermissionScope.Workspace,
                "a" or "always" => PermissionScope.Always,
                _ => null,
            };

            // Default: approve with the first available scope.
            scope ??= request.AllowedScopes.Count > 0 ? request.AllowedScopes[0] : PermissionScope.Once;

            Resolve(new PermissionResponse(Granted: true, Scope: scope));
            return true;
        }

        // Text editing in the permission prompt.
        if (bare == KeyCode.Backspace)
            View.Editor.Backspace();
        else if (args.KeyChar >= ' ' && args.KeyChar != '\0' && !args.KeyCode.HasCtrl())
            View.Editor.InsertChar(args.KeyChar);

        return true;
    }

    /// <summary>
    /// Renders the prompt at the given position. No-op when inactive so the
    /// caller can always call this unconditionally.
    /// </summary>
    public void Render(ConsoleBuffer buffer, int x, int y, int width)
    {
        if (IsActive)
            View.Render(buffer, x, y, width);
    }

    private void Resolve(PermissionResponse response)
    {
        View.ActiveRequest = null;
        View.Editor.Clear();
        _tcs?.TrySetResult(response);
        _tcs = null;
    }
}
