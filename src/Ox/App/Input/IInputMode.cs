using Ox.Terminal.Input;

namespace Ox.App.Input;

/// <summary>
/// Result of dispatching a key to an input mode. Tells the router whether the
/// key was consumed by the mode or should bubble up to the next handler —
/// used today only for passthrough keys the mode explicitly wants to ignore,
/// though the shape anticipates richer routing as more floating panels are added.
/// </summary>
internal enum KeyHandled
{
    /// <summary>The mode handled the key; stop dispatching.</summary>
    Yes,

    /// <summary>The mode did not handle the key; try the next handler (or let it fall through).</summary>
    PassThrough,
}

/// <summary>
/// A single input-handling surface. The router picks exactly one active mode
/// per keystroke (permission prompt &gt; connect wizard &gt; chat) so input is
/// naturally modal — the active floating panel "wins" over the composer.
///
/// Splitting input handling by mode lets each one own only the keys it cares
/// about. Previously <see cref="OxApp"/> had a single
/// <see cref="OxApp">HandleKey</see> that branched on whether any floating
/// panel was active; every new panel added another if-ladder branch to it.
/// </summary>
internal interface IInputMode
{
    /// <summary>Whether this mode should receive input right now.</summary>
    bool IsActive { get; }

    /// <summary>Handle a single keypress. See <see cref="KeyHandled"/>.</summary>
    KeyHandled HandleKey(KeyEventArgs args);
}
