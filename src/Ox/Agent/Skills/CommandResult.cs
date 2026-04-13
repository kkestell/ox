namespace Ox.Agent.Skills;

/// <summary>
/// The outcome of executing a built-in slash command.
///
/// Produced by <see cref="Sessions.OxSession.ExecuteBuiltInCommand"/> and
/// consumed by the TUI layer (OxApp) to render feedback to the user.
/// Carrying only a message and an error flag keeps internal Ur state encapsulated
/// — the caller never needs to know which configuration object was mutated.
/// </summary>
public sealed record CommandResult(string Message, bool IsError = false);
