using Ox.App.Permission;
using Ox.Terminal.Input;

namespace Ox.App.Input;

/// <summary>
/// Input mode active while a permission prompt is visible. Delegates all
/// keystrokes to <see cref="PermissionPromptBridge.HandleKey"/> so the modal
/// dialog is the only consumer of input until the user responds.
/// </summary>
internal sealed class PermissionInputMode(PermissionPromptBridge bridge) : IInputMode
{
    public bool IsActive => bridge.IsActive;

    public KeyHandled HandleKey(KeyEventArgs args)
    {
        bridge.HandleKey(args);
        return KeyHandled.Yes;
    }
}
