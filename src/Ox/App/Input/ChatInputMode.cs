using Ox.Terminal.Input;

namespace Ox.App.Input;

/// <summary>
/// Default input mode — the chat composer. Active whenever no modal panel
/// (permission prompt, connect wizard) intercepts input.
///
/// Handles: global shortcuts (Ctrl+C exit, Ctrl+D empty-buffer exit, screen
/// dump, Escape during turn), autocomplete (Tab), submit (Enter), basic text
/// editing (Backspace, Delete, arrows, Home, End), and printable-character
/// insertion.
///
/// The mode collaborates through five callbacks rather than holding
/// references to the coordinator — each callback is the narrowest surface
/// the mode needs from OxApp, which keeps the unit boundary clean:
///
/// - <paramref name="requestExit"/> flips OxApp's exit flag.
/// - <paramref name="onScreenDumpShortcut"/> captures the screen to a file.
/// - <paramref name="cancelTurn"/> runs when the user presses Escape mid-turn.
/// - <paramref name="tryApplyAutocomplete"/> attempts a ghost-text accept on Tab.
/// - <paramref name="validateAndTake"/> runs the composer's current text
///   (validation for /model lives here so an invalid argument blocks Enter entirely).
/// </summary>
internal sealed class ChatInputMode(
    TextEditor editor,
    Func<bool> turnActive,
    Action requestExit,
    Action cancelTurn,
    Action<int> onScreenDumpShortcut,
    Action tryApplyAutocomplete,
    Func<string> validateAndTake) : IInputMode
{
    // ChatInputMode is the last-resort handler — the router only reaches it
    // after the permission and wizard modes pass on input — so it is always
    // "active" from the router's perspective.
    public bool IsActive => true;

    public KeyHandled HandleKey(KeyEventArgs args)
    {
        var keyCode = args.KeyCode;
        var bare = keyCode.WithoutModifiers();

        // Global shortcut: Ctrl+C always exits, even while the composer is focused.
        if (bare == KeyCode.C && keyCode.HasCtrl())
        {
            requestExit();
            return KeyHandled.Yes;
        }

        // Screen dump shortcuts (e.g. Ctrl+Shift+S) — raw keycode handoff so
        // the recognizer can match against its own table.
        if (ScreenDumpWriter.IsDumpShortcut((int)keyCode))
        {
            onScreenDumpShortcut((int)keyCode);
            return KeyHandled.Yes;
        }

        // Escape during active turn → cancel.
        if (bare == KeyCode.Esc && turnActive())
        {
            cancelTurn();
            return KeyHandled.Yes;
        }

        // Ctrl+D on an empty buffer exits; Ctrl+D with text is consumed as a no-op.
        if (bare == KeyCode.D && keyCode.HasCtrl())
        {
            if (editor.Text.Length == 0)
                requestExit();
            return KeyHandled.Yes;
        }

        // Tab → autocomplete.
        if (bare == KeyCode.Tab)
        {
            tryApplyAutocomplete();
            return KeyHandled.Yes;
        }

        // Enter → submit. validateAndTake returns empty when validation fails
        // (e.g. an unknown /model argument) so submission is blocked silently.
        if (bare == KeyCode.Enter)
        {
            var text = validateAndTake();
            // The submit callback itself already happens inside validateAndTake
            // so we don't need to do anything else here — using the sentinel
            // return value only to keep the API straight.
            _ = text;
            return KeyHandled.Yes;
        }

        // Text editing keys.
        switch (bare)
        {
            case KeyCode.Backspace:
                editor.Backspace();
                return KeyHandled.Yes;
            case KeyCode.Delete:
                editor.Delete();
                return KeyHandled.Yes;
            case KeyCode.CursorLeft:
                editor.MoveLeft();
                return KeyHandled.Yes;
            case KeyCode.CursorRight:
                editor.MoveRight();
                return KeyHandled.Yes;
            case KeyCode.Home:
                editor.Home();
                return KeyHandled.Yes;
            case KeyCode.End:
                editor.End();
                return KeyHandled.Yes;
        }

        // Printable character insertion.
        if (args.KeyChar >= ' ' && args.KeyChar != '\0' && !keyCode.HasCtrl() && !keyCode.HasAlt())
        {
            editor.InsertChar(args.KeyChar);
        }

        return KeyHandled.Yes;
    }
}
