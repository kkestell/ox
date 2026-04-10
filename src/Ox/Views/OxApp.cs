using Terminal.Gui.App;
using Terminal.Gui.Configuration;
using Terminal.Gui.Drawing;
using Terminal.Gui.Input;
using Terminal.Gui.ViewBase;
using Terminal.Gui.Views;
using Ur.Todo;

namespace Ox.Views;

/// <summary>
/// The root window for the Ox TUI application. Manages the three-panel layout:
///   - ConversationView (top, fills available space)
///   - InputAreaView (bottom, fixed 5 rows)
///   - SidebarView (right, optional, max 36 cols or 1/3 width)
///
/// Input handling is delegated to InputAreaView, which uses a real TextField
/// for text editing. The REPL loop reads submitted lines via
/// <see cref="InputAreaView.ReadLineAsync"/>.
/// </summary>
internal sealed class OxApp : Window
{
    // Fixed height for the input area: top border + text + divider + status + bottom border.
    private const int InputAreaHeight = 5;

    // Maximum sidebar width, and the fraction of terminal width it can occupy.
    private const int MaxSidebarWidth = 36;

    private readonly IApplication _app;
    private readonly string _workspacePath;
    private readonly ConversationView _conversationView;
    private readonly InputAreaView _inputAreaView;
    private readonly SidebarView _sidebarView;

    /// <summary>The Terminal.Gui application instance for thread marshalling.</summary>
    public IApplication App => _app;

    public ConversationView ConversationView => _conversationView;
    public InputAreaView InputAreaView => _inputAreaView;
    public SidebarView SidebarView => _sidebarView;

    public OxApp(IApplication app, TodoStore todoStore, string workspacePath)
    {
        _app = app;
        _workspacePath = workspacePath;
        // Remove the default Window border — we draw our own chrome.
        BorderStyle = LineStyle.None;
        Title = "";

        // Force a solid black background across the entire app. All child views
        // inherit this scheme, so Color.None and Attribute.Default resolve to
        // black instead of the terminal's default background.
        var palette = OxThemePalette.Ox;
        var whiteOnBlack = new Terminal.Gui.Drawing.Attribute(
            ToTerminalColor(palette.NormalForeground),
            ToTerminalColor(palette.NormalBackground));
        var oxScheme = new Scheme(whiteOnBlack)
        {
            Focus = whiteOnBlack,
            // Terminal.Gui derives the Editable role by dimming Normal's
            // foreground into the background, which turns white-on-black into
            // a gray editor surface. Ox uses editable controls inside a black
            // canvas, so pin Editable to the same black-backed attribute.
            Editable = new Terminal.Gui.Drawing.Attribute(
                ToTerminalColor(palette.EditableForeground),
                ToTerminalColor(palette.EditableBackground))
        };
        SchemeManager.AddScheme("Ox", oxScheme);
        SchemeName = "Ox";

        CanFocus = true;

        _conversationView = new ConversationView(app);
        _inputAreaView = new InputAreaView(app);
        _sidebarView = new SidebarView(app, todoStore);

        // Sidebar starts hidden (no content yet).
        _sidebarView.Visible = false;

        // Build the layout. The sidebar sits on the right edge; conversation and
        // input fill the remaining left space. Dim.Func computes widths dynamically
        // so toggling sidebar visibility triggers automatic relayout.
        _sidebarView.Width = Dim.Func(ComputeSidebarWidth, this);
        _sidebarView.X = Pos.Func(v => v.Frame.Width - ComputeSidebarWidth(v), this);
        _sidebarView.Y = 0;
        _sidebarView.Height = Dim.Fill();

        _conversationView.X = 0;
        _conversationView.Y = 0;
        _conversationView.Width = Dim.Func(ComputeMainWidth, this);
        _conversationView.Height = Dim.Fill(Dim.Absolute(InputAreaHeight));

        _inputAreaView.X = 0;
        _inputAreaView.Y = Pos.AnchorEnd(InputAreaHeight);
        _inputAreaView.Width = Dim.Func(ComputeMainWidth, this);
        _inputAreaView.Height = Dim.Absolute(InputAreaHeight);

        Add(_conversationView, _inputAreaView, _sidebarView);

        // When sidebar visibility changes, force the layout to recalculate.
        _sidebarView.VisibleChanged += (_, _) => SetNeedsLayout();
        KeyDown += OnKeyDown;
    }

    /// <summary>
    /// Computes the width of the main area (conversation + input), accounting
    /// for the sidebar when it's visible.
    /// </summary>
    private int ComputeMainWidth(View? container)
    {
        if (container is null) return 80;
        if (_sidebarView.Visible)
        {
            var sidebarW = ComputeSidebarWidth(container);
            return Math.Max(1, container.Frame.Width - sidebarW);
        }
        return container.Frame.Width;
    }

    /// <summary>
    /// Computes the sidebar width: up to 1/3 of terminal width, capped at MaxSidebarWidth.
    /// Returns 0 when the sidebar is hidden.
    /// </summary>
    private int ComputeSidebarWidth(View? container)
    {
        if (container is null || !_sidebarView.Visible)
            return 0;
        return Math.Min(MaxSidebarWidth, container.Frame.Width / 3);
    }

    private void OnKeyDown(object? sender, Key key)
    {
        if (!ScreenDumpWriter.IsDumpShortcut((int)key.KeyCode))
            return;

        // Dump the driver's rendered screen, not Ox's logical model, so the file
        // reflects the exact terminal state the user is reporting.
        _ = ScreenDumpWriter.Write(_workspacePath, _app.ToString(), DateTimeOffset.Now);
        key.Handled = true;
    }

    private static Color ToTerminalColor(OxThemeColor color) =>
        color switch
        {
            OxThemeColor.Black => new Color(ColorName16.Black),
            OxThemeColor.White => new Color(ColorName16.White),
            _ => throw new ArgumentOutOfRangeException(nameof(color), color, "Unsupported Ox theme color.")
        };

}
