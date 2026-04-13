using Ox.Terminal.Input;

namespace Ox.App.Input;

/// <summary>
/// Dispatches keystrokes to the topmost active <see cref="IInputMode"/>.
///
/// Modes are tried in the order they're registered; the first one whose
/// <see cref="IInputMode.IsActive"/> returns true handles the key. Ordering
/// matters — modal surfaces (permission prompt, connect wizard) are registered
/// before the chat composer so they intercept input whenever visible.
///
/// Keeps the router trivial on purpose: the interesting logic lives in each
/// mode. The router's only job is "given the current activation state, who
/// owns this key?" — contrast with OxApp's previous if-ladder that mixed
/// global shortcuts, modal precedence, and composer keys in one method.
/// </summary>
internal sealed class InputRouter(IReadOnlyList<IInputMode> modes)
{
    public void HandleKey(KeyEventArgs args)
    {
        foreach (var mode in modes)
        {
            if (!mode.IsActive)
                continue;

            if (mode.HandleKey(args) == KeyHandled.Yes)
                return;
        }
    }
}
