using Terminal.Gui.App;
using Terminal.Gui.Configuration;
using Terminal.Gui.Drawing;
using Terminal.Gui.Input;
using Terminal.Gui.ViewBase;
using Terminal.Gui.Views;

namespace Ox.Views;

/// <summary>
/// The root window for the Ox TUI application. Manages the two-region layout:
///   - ConversationView (top, fills available space)
///   - InputAreaView (bottom, fixed 5 rows)
///
/// Owns the ComposerController and wires it to InputAreaView so that all
/// composer workflow coordination flows through a single well-defined seam:
/// the view emits raw user intents, the controller interprets them, and the
/// REPL loop and PermissionHandler consume the results.
/// </summary>
internal sealed class OxApp : Window
{
    // Fixed height for the input area: top border + text + divider + status + bottom border.
    private const int InputAreaHeight = 5;

    private readonly IApplication _app;
    private readonly string _workspacePath;
    private readonly SplashView _splashView;
    private readonly ConversationView _conversationView;
    private readonly InputAreaView _inputAreaView;
    private readonly ComposerController _composerController;

    /// <summary>The Terminal.Gui application instance for thread marshalling.</summary>
    public IApplication App => _app;

    public ConversationView ConversationView => _conversationView;
    public InputAreaView InputAreaView => _inputAreaView;

    /// <summary>
    /// The composer workflow coordinator. Exposes the chat submission channel
    /// consumed by the REPL loop and the permission session API used by
    /// PermissionHandler.
    /// </summary>
    public ComposerController ComposerController => _composerController;

    public OxApp(IApplication app, string workspacePath)
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

        _splashView = new SplashView();
        _conversationView = new ConversationView();
        _inputAreaView = new InputAreaView(app);

        // Create the controller and bind it to the view so Enter and EOF
        // signals are routed before the event loop starts processing input.
        _composerController = new ComposerController();
        _inputAreaView.BindController(_composerController);

        // Splash occupies the same space as the conversation view. It's shown
        // when the conversation is empty and hidden once the first entry arrives.
        _splashView.X = 0;
        _splashView.Y = 0;
        _splashView.Width = Dim.Fill();
        _splashView.Height = Dim.Fill(Dim.Absolute(InputAreaHeight));

        _conversationView.X = 0;
        _conversationView.Y = 0;
        _conversationView.Width = Dim.Fill();
        _conversationView.Height = Dim.Fill(Dim.Absolute(InputAreaHeight));
        _conversationView.Visible = false; // Hidden until first entry

        _inputAreaView.X = 0;
        _inputAreaView.Y = Pos.AnchorEnd(InputAreaHeight);
        _inputAreaView.Width = Dim.Fill();
        _inputAreaView.Height = Dim.Absolute(InputAreaHeight);

        Add(_splashView, _conversationView, _inputAreaView);

        // Toggle splash/conversation visibility when content arrives.
        _conversationView.ContentChanged += OnConversationContentChanged;
        KeyDown += OnKeyDown;
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

    /// <summary>
    /// Switches from splash to conversation view when the first entry is added.
    /// </summary>
    private void OnConversationContentChanged()
    {
        if (_conversationView.EntryCount > 0 && !_conversationView.Visible)
        {
            _splashView.Visible = false;
            _conversationView.Visible = true;
        }
    }

    private static Color ToTerminalColor(OxThemeColor color) =>
        color switch
        {
            OxThemeColor.Black => new Color(ColorName16.Black),
            OxThemeColor.White => new Color(ColorName16.White),
            _ => throw new ArgumentOutOfRangeException(nameof(color), color, "Unsupported Ox theme color.")
        };

}
