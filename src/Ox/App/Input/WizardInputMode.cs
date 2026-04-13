using Ox.App.Connect;
using Ox.Terminal.Input;

namespace Ox.App.Input;

/// <summary>
/// Input mode active while the connect wizard is open.
///
/// The wizard has three steps with different key semantics: list steps (provider,
/// model) accept Up/Down/Enter; the API key step accepts a full text-editor
/// keymap. This mode encapsulates those branches so <see cref="OxApp"/> just
/// wires one routing rule rather than six.
///
/// <paramref name="advanceStep"/> is invoked on Enter for list steps; it's a
/// callback rather than a direct method because "advance" pulls data from
/// <see cref="App.Configuration.ModelCatalog"/> / <see cref="Agent.Hosting.OxHost"/>
/// that the mode has no business holding references to. Similarly,
/// <paramref name="requestExit"/> is called when Escape is pressed during a
/// required (first-run) wizard, because only the coordinator knows how to
/// shut the app down cleanly.
/// </summary>
internal sealed class WizardInputMode(
    ConnectWizardController wizard,
    Action advanceStep,
    Action requestExit) : IInputMode
{
    public bool IsActive => wizard.IsActive;

    public KeyHandled HandleKey(KeyEventArgs args)
    {
        var bare = args.KeyCode.WithoutModifiers();

        // Escape cancels the wizard. When required (first run), cancelling
        // means there is no config to fall back to, so the app should exit.
        if (bare == KeyCode.Esc)
        {
            wizard.Cancel();
            if (wizard.IsRequired)
                requestExit();
            return KeyHandled.Yes;
        }

        switch (wizard.CurrentStep)
        {
            case WizardStep.SelectProvider:
            case WizardStep.SelectModel:
                HandleListInput(bare);
                break;

            case WizardStep.EnterApiKey:
                HandleKeyInput(args, bare);
                break;
        }

        return KeyHandled.Yes;
    }

    private void HandleListInput(KeyCode bare)
    {
        switch (bare)
        {
            case KeyCode.CursorUp:
                wizard.NavigateUp();
                break;

            case KeyCode.CursorDown:
                wizard.NavigateDown();
                break;

            case KeyCode.Enter:
                advanceStep();
                break;
        }
    }

    private void HandleKeyInput(KeyEventArgs args, KeyCode bare)
    {
        if (bare == KeyCode.Enter)
        {
            wizard.TryConfirmApiKey();
            return;
        }

        if (bare == KeyCode.Backspace)
        {
            wizard.BackspaceApiKey();
            return;
        }

        if (bare == KeyCode.Delete)
        {
            wizard.DeleteApiKey();
            return;
        }

        if (bare == KeyCode.CursorLeft) { wizard.KeyEditor.MoveLeft(); return; }
        if (bare == KeyCode.CursorRight) { wizard.KeyEditor.MoveRight(); return; }
        if (bare == KeyCode.Home) { wizard.KeyEditor.Home(); return; }
        if (bare == KeyCode.End) { wizard.KeyEditor.End(); return; }

        // Printable character.
        if (args.KeyChar >= ' ' && args.KeyChar != '\0' && !args.KeyCode.HasCtrl() && !args.KeyCode.HasAlt())
            wizard.InsertApiKeyChar(args.KeyChar);
    }
}
