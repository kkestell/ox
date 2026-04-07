namespace Ur.Skills;

/// <summary>
/// Fixed catalog of built-in slash commands defined in the Ur runtime.
///
/// Registration order determines autocomplete priority — when two names share a
/// prefix, the first registered name is suggested. Built-ins are registered
/// before skills so that first-party commands always win prefix conflicts.
///
/// The stubs at the bottom exist purely to exercise the multi-match autocomplete
/// path during development (e.g. /c matches /clear and /clamp, /m matches /model,
/// /memo, and /models). Delete them once autocomplete has been manually verified.
/// </summary>
public sealed class BuiltInCommandRegistry
{
    private readonly Dictionary<string, BuiltInCommand> _byName;

    /// <summary>All registered built-in commands in registration order.</summary>
    public IReadOnlyList<BuiltInCommand> All { get; }

    public BuiltInCommandRegistry()
    {
        // Registration order defines autocomplete priority within built-ins.
        // Fully-implemented commands first; stubs appended at the end.
        var commands = new List<BuiltInCommand>
        {
            new("clear",    "Clear conversation history"),
            new("model",    "Switch the active model"),
            new("quit",     "Exit the session"),
            new("set",      "Configure a session setting"),

            // Autocomplete-testing stubs — remove after manual verification.
            new("clamp",    "stub"),
            new("memo",     "stub"),
            new("models",   "stub"),
            new("quickfix", "stub"),
            new("query",    "stub"),
        };

        All = commands.AsReadOnly();
        // Case-insensitive so "/Clear" and "/clear" both resolve.
        _byName = commands.ToDictionary(c => c.Name, StringComparer.OrdinalIgnoreCase);
    }

    /// <summary>Looks up a built-in command by name (case-insensitive). Returns null if not found.</summary>
    public BuiltInCommand? Get(string name) =>
        _byName.GetValueOrDefault(name);
}
