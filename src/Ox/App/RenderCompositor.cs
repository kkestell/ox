using Ox.App.Connect;
using Ox.App.Input;
using Ox.App.Permission;
using Ox.App.Views;
using Ox.Terminal.Rendering;

namespace Ox.App;

/// <summary>
/// Owns the frame surface and composes one render pass per loop iteration.
///
/// Splitting this off <see cref="OxApp"/> means the coordinator no longer has
/// to hold the buffer, the static view instances (<see cref="InputAreaView"/>,
/// <see cref="ConnectWizardView"/>), or the workspace path purely to stage the
/// screen-dump filename. Rendering and the two things that are structurally
/// part of rendering (terminal-resize detection, screen-dump capture) sit
/// together here.
///
/// The compositor renders from parameters rather than from fields so that
/// <see cref="OxApp"/> retains ownership of dynamic state (session, turn
/// controller, text editor). That keeps the coupling one-way and leaves the
/// compositor safe to unit-test by pointing it at a fake buffer.
/// </summary>
internal sealed class RenderCompositor
{
    private readonly ConsoleBuffer _buffer;
    private readonly InputAreaView _inputAreaView = new();
    private readonly ConnectWizardView _wizardView = new();
    private readonly string _workspacePath;

    public RenderCompositor(int width, int height, string workspacePath, Color background)
    {
        _buffer = new ConsoleBuffer(width, height);
        _workspacePath = workspacePath;

        // Force an explicit background for every cell that uses Color.Default.
        // Without this, empty cells emit SGR 49 ("terminal default"), which is
        // whatever color the user configured in their terminal — usually not
        // the theme's intended background.
        _buffer.DefaultBackgroundOverride = background;
    }

    /// <summary>
    /// Re-sizes the underlying buffer if the terminal size has changed. Called
    /// once at the top of each main-loop iteration.
    /// </summary>
    public void CheckResize()
    {
        var (width, height) = GetTerminalSize();
        if (width != _buffer.Width || height != _buffer.Height)
            _buffer.Resize(width, height);
    }

    /// <summary>
    /// Composes and flushes one frame. Render order matters: conversation
    /// first, then the optional permission prompt, then the input area, and
    /// finally the wizard overlay if visible.
    /// </summary>
    public void Render(
        ConversationView conversationView,
        PermissionPromptBridge permissionBridge,
        TextEditor editor,
        Autocomplete autocomplete,
        Throbber throbber,
        ConnectWizardController wizard,
        bool turnActive,
        int? contextPercent,
        string? statusModelId)
    {
        _buffer.Clear();

        var width = _buffer.Width;
        var height = _buffer.Height;

        // Reserve the composer's shadow gutter so the slab can float above the
        // terminal edge instead of clipping its right and bottom cast.
        var conversationHeight = Math.Max(0, height - InputAreaView.Height - InputAreaView.ShadowHeight);
        conversationView.Render(_buffer, 0, 0, width, conversationHeight);

        // Permission prompt: floats above the input area when active.
        if (permissionBridge.IsActive)
        {
            var promptX = InputAreaView.HorizontalMargin;
            var promptY = Math.Max(0, conversationHeight - PermissionPromptView.Height - PermissionPromptView.ShadowHeight);
            var promptWidth = Math.Max(
                4,
                width - (InputAreaView.HorizontalMargin * 2) - InputAreaView.ShadowWidth);
            permissionBridge.Render(_buffer, promptX, promptY, promptWidth);
        }

        // Input area: fixed at the bottom.
        var inputX = InputAreaView.HorizontalMargin;
        var inputY = Math.Max(0, height - InputAreaView.Height - InputAreaView.ShadowHeight);
        var inputWidth = Math.Max(
            4,
            width - (InputAreaView.HorizontalMargin * 2) - InputAreaView.ShadowWidth);
        var ghostText = autocomplete.GetGhostText(editor.Text);
        var statusRight = InputStatusFormatter.Compose(contextPercent, statusModelId);

        _inputAreaView.Render(
            _buffer,
            inputX, inputY, inputWidth,
            editor,
            ghostText,
            statusRight,
            turnActive ? throbber : null,
            !permissionBridge.IsActive && !wizard.IsActive);

        // Connect wizard: floats centred over everything as the final draw
        // pass so it appears on top of the conversation area and input chrome.
        if (wizard.IsActive)
            _wizardView.Render(_buffer, wizard);

        _buffer.Render(Console.Out);
    }

    /// <summary>
    /// Writes a text-only snapshot of the current frame to the workspace's
    /// screen-dump directory. Driven by the Ctrl+Shift+S shortcut.
    /// </summary>
    public void CaptureScreenDump()
    {
        var lines = new string[_buffer.Height];
        for (var row = 0; row < _buffer.Height; row++)
        {
            var chars = new char[_buffer.Width];
            for (var col = 0; col < _buffer.Width; col++)
                chars[col] = _buffer.GetRenderedCell(col, row).Rune;
            lines[row] = new string(chars).TrimEnd();
        }

        var screenText = string.Join('\n', lines);
        ScreenDumpWriter.Write(_workspacePath, screenText, DateTimeOffset.UtcNow);
    }

    // Clamp to a floor so a collapsed terminal doesn't crash layout math.
    private static (int Width, int Height) GetTerminalSize()
    {
        var width = Console.WindowWidth;
        var height = Console.WindowHeight;
        return (Math.Max(20, width), Math.Max(10, height));
    }
}
