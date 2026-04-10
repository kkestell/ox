using System.Text;
using Terminal.Gui.App;
using Terminal.Gui.Configuration;
using Terminal.Gui.Drawing;
using Terminal.Gui.Drivers;
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
/// Also handles keyboard input by processing keys in OnKeyDown and maintaining
/// the input buffer. The REPL loop in TuiService reads submitted lines via
/// <see cref="ReadLineAsync"/>, which uses a TaskCompletionSource to bridge
/// the event-driven key handling with the async REPL loop.
/// </summary>
internal sealed class OxApp : Window
{
    // Fixed height for the input area: top border + text + divider + status + bottom border.
    private const int InputAreaHeight = 5;

    // Maximum sidebar width, and the fraction of terminal width it can occupy.
    private const int MaxSidebarWidth = 36;

    private readonly IApplication _app;
    private readonly ConversationView _conversationView;
    private readonly InputAreaView _inputAreaView;
    private readonly SidebarView _sidebarView;

    // Input buffer and completion state managed by key handling.
    private readonly StringBuilder _inputBuffer = new();
    private string _promptPrefix = "";
    private AutocompleteEngine? _autocomplete;

    // The TCS that ReadLineAsync awaits. Set when the user presses Enter, Ctrl+C, or Ctrl+D.
    private TaskCompletionSource<string?>? _inputTcs;

    // Callback for completion changes (ghost text), wired by ReadLineAsync.
    private Action<string?>? _onCompletionChanged;

    /// <summary>The Terminal.Gui application instance for thread marshalling.</summary>
    public IApplication App => _app;

    public ConversationView ConversationView => _conversationView;
    public InputAreaView InputAreaView => _inputAreaView;
    public SidebarView SidebarView => _sidebarView;

    public OxApp(IApplication app, TodoStore todoStore)
    {
        _app = app;
        // Remove the default Window border — we draw our own chrome.
        BorderStyle = LineStyle.None;
        Title = "";

        // Force a solid black background across the entire app. All child views
        // inherit this scheme, so Color.None and Attribute.Default resolve to
        // black instead of the terminal's default background.
        var black = new Color(ColorName16.Black);
        var white = new Color(ColorName16.White);
        var oxScheme = new Scheme(new Terminal.Gui.Drawing.Attribute(white, black));
        SchemeManager.AddScheme("Ox", oxScheme);
        SchemeName = "Ox";

        // OxApp must be focusable so it receives keyboard events.
        // All child views are non-focusable (CanFocus=false) — keyboard
        // input is processed centrally by OxApp.OnKeyDown.
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

        // Subscribe to the view's own KeyDown event. Since OxApp is the top-level
        // Window and CanFocus=true, it receives all unhandled keys.
        KeyDown += OnApplicationKeyDown;
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

    /// <summary>
    /// Sets the autocomplete engine for slash-command ghost text.
    /// </summary>
    public void SetAutocomplete(AutocompleteEngine engine)
    {
        _autocomplete = engine;
    }

    /// <summary>
    /// Reads a line of user input through the TUI. Returns the submitted text on
    /// Enter, or null on EOF (Ctrl+C/Ctrl+D on empty buffer) or cancellation.
    ///
    /// The prompt prefix is shown before the user's typed text (used for permission
    /// prompts like "Allow 'bash' to run 'ls'? (y/n): ").
    ///
    /// This method awaits a TaskCompletionSource that is completed by OnKeyDown
    /// when the user presses Enter or an EOF key combo.
    /// </summary>
    public async Task<string?> ReadLineAsync(
        string promptPrefix,
        Action<string?>? onCompletionChanged = null,
        CancellationToken ct = default)
    {
        _promptPrefix = promptPrefix;
        _inputBuffer.Clear();
        _onCompletionChanged = onCompletionChanged;
        _inputTcs = new TaskCompletionSource<string?>();

        // Show the initial prompt
        _inputAreaView.SetPrompt(promptPrefix);
        onCompletionChanged?.Invoke(null);

        // Register cancellation
        await using var reg = ct.Register(() =>
        {
            _app.Invoke(() => _inputTcs?.TrySetResult(null));
        });

        return await _inputTcs.Task;
    }

    /// <summary>
    /// Handles all keyboard input for the application. Processes typing, submission,
    /// and control keys for the input buffer. Subscribed to Application.KeyDown so
    /// it fires at the application level regardless of focus state.
    /// </summary>
    private void OnApplicationKeyDown(object? sender, Key key)
    {
        // If no input TCS is active, we're not reading input — let it pass through.
        if (_inputTcs is null || _inputTcs.Task.IsCompleted)
            return;

        var keyCode = key.KeyCode;

        // Enter: submit the current buffer
        if (keyCode == KeyCode.Enter)
        {
            _onCompletionChanged?.Invoke(null);
            var result = _inputBuffer.ToString();
            _inputTcs.TrySetResult(result);
            key.Handled = true;
            return;
        }

        // Backspace: delete last character
        if (keyCode == KeyCode.Backspace)
        {
            if (_inputBuffer.Length > 0)
                _inputBuffer.Remove(_inputBuffer.Length - 1, 1);
            _onCompletionChanged?.Invoke(null);
            UpdatePromptDisplay();
            key.Handled = true;
            return;
        }

        // Tab: accept autocomplete suggestion
        if (keyCode == KeyCode.Tab)
        {
            var suffix = _autocomplete?.GetCompletion(_inputBuffer.ToString());
            if (suffix is not null)
            {
                _inputBuffer.Append(suffix);
                _onCompletionChanged?.Invoke(null);
                UpdatePromptDisplay();
            }
            key.Handled = true;
            return;
        }

        // Ctrl+C: EOF signal
        if (keyCode == (KeyCode.C | KeyCode.CtrlMask))
        {
            _onCompletionChanged?.Invoke(null);
            _inputTcs.TrySetResult(null);
            key.Handled = true;
            return;
        }

        // Ctrl+D on empty buffer: EOF
        if (keyCode == (KeyCode.D | KeyCode.CtrlMask) && _inputBuffer.Length == 0)
        {
            _inputTcs.TrySetResult(null);
            key.Handled = true;
            return;
        }

        // Escape: no-op during input (escape cancellation is turn-level, not input-level)
        if (keyCode == KeyCode.Esc)
        {
            key.Handled = true;
            return;
        }

        // Regular character input
        var rune = key.AsRune;
        if (rune != default && !char.IsControl((char)rune.Value))
        {
            _inputBuffer.Append((char)rune.Value);
            UpdatePromptDisplay();
            key.Handled = true;
        }
    }

    /// <summary>
    /// Updates the input area display with the current prompt prefix + buffer,
    /// and recomputes the autocomplete ghost text.
    /// </summary>
    private void UpdatePromptDisplay()
    {
        _inputAreaView.SetPrompt(_promptPrefix + _inputBuffer);
        _onCompletionChanged?.Invoke(_autocomplete?.GetCompletion(_inputBuffer.ToString()));
    }
}
