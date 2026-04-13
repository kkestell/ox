namespace Ox.Agent.Skills;

/// <summary>
/// Fixed catalog of built-in slash commands defined in the Ox agent runtime.
///
/// Registration order determines autocomplete priority — when two names share a
/// prefix, the first registered name is suggested. Built-ins are registered
/// before skills so that first-party commands always win prefix conflicts.
/// </summary>
public sealed class BuiltInCommandRegistry
{
    private readonly Dictionary<string, BuiltInCommand> _byName;

    /// <summary>All registered built-in commands in registration order.</summary>
    public IReadOnlyList<BuiltInCommand> All { get; }

    public BuiltInCommandRegistry()
    {
        // Registration order defines autocomplete priority within built-ins.
        var commands = new List<BuiltInCommand>
        {
            new("clear"),
            new("connect"),
            new("model"),
            new("quit"),
            new("set"),
        };

        All = commands.AsReadOnly();
        // Case-insensitive so "/Clear" and "/clear" both resolve.
        _byName = commands.ToDictionary(c => c.Name, StringComparer.OrdinalIgnoreCase);
    }

    /// <summary>Looks up a built-in command by name (case-insensitive). Returns null if not found.</summary>
    public BuiltInCommand? Get(string name) =>
        _byName.GetValueOrDefault(name);
}
