namespace Te.Input;

/// <summary>
/// Configuration for the built-in terminal input source.
/// The options stay intentionally small so Te exposes only the parts of the
/// terminal protocol we have chosen to support in this extraction.
/// </summary>
public sealed class TerminalInputSourceOptions
{
    /// <summary>
    /// Enables SGR mouse reporting on Unix-like terminals.
    /// This has no effect on platforms where Te currently falls back to managed
    /// keyboard polling.
    /// </summary>
    public bool EnableMouse { get; init; } = true;

    /// <summary>
    /// Enables mouse motion reporting with no buttons pressed.
    /// Disabled by default because it creates a high-volume event stream that is
    /// not necessary for Te's current demo and base abstractions.
    /// </summary>
    public bool EnableMouseMove { get; init; }

    /// <summary>
    /// Poll timeout for the Unix raw input loop and the managed keyboard fallback.
    /// Short polling keeps Escape-key disambiguation responsive without turning the
    /// input source into a busy spin loop.
    /// </summary>
    public int PollTimeoutMs { get; init; } = 25;
}
