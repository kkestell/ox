using Ox.Agent.Sessions;
using Ox.Agent.Skills;
using Ox.App.Configuration;

namespace Ox.App.Commands;

/// <summary>
/// What the coordinator should do after the dispatcher handles a submitted line.
///
/// The dispatcher covers the TUI's slash-command surface (<c>/quit</c>,
/// <c>/connect</c>, built-ins, user-invocable skills) plus plain text. Each
/// return value maps to a single, well-defined action OxApp takes — no branches
/// inside the coordinator except a switch on this value.
/// </summary>
internal abstract record CommandOutcome
{
    /// <summary>The command is done — OxApp should neither start a turn nor show an error.</summary>
    public sealed record Handled(bool InvalidatedContextWindow) : CommandOutcome;

    /// <summary>Run a normal turn with the given text (plain input or an expanded skill).</summary>
    public sealed record StartTurn(string Text) : CommandOutcome;

    /// <summary>The command was not recognized — OxApp should surface an error entry.</summary>
    public sealed record Unknown(string Name) : CommandOutcome;

    /// <summary>The user asked to exit (e.g. <c>/quit</c>).</summary>
    public sealed record Exit : CommandOutcome;

    /// <summary>The user asked to open the connect wizard (e.g. <c>/connect</c>).</summary>
    public sealed record OpenWizard : CommandOutcome;
}

/// <summary>
/// Slash-command parsing and dispatch for the TUI submit path.
///
/// Owns the composition of "is this text a slash command? which kind?" —
/// previously a nested conditional inside <see cref="OxApp">OxApp.SubmitInput</see>.
/// Returning a <see cref="CommandOutcome"/> keeps OxApp's submit flow linear:
/// it calls <see cref="Dispatch"/>, then switches on the outcome.
///
/// The dispatcher does not manipulate UI state itself — it only reads the
/// command name and consults the registries. Side effects (showing an error,
/// starting a turn, opening the wizard, exiting) are the coordinator's job.
///
/// <see cref="TryValidateForSubmission"/> is an up-front check used by the
/// input handler before Enter submission — currently only to block /model
/// with an unknown argument so <see cref="OxSession.ExecuteBuiltInCommand"/>
/// is never called with garbage. Validation is a property of the command, so
/// it lives next to the dispatch logic rather than duplicated on OxApp.
/// </summary>
internal sealed class CommandDispatcher(
    Func<OxSession?> sessionSource,
    CommandRegistry commandRegistry,
    ModelCatalog modelCatalog)
{
    private readonly IReadOnlyList<string> _validModelIds = modelCatalog.ListAllModelIds();

    /// <summary>
    /// Returns true when the given composer text is safe to dispatch. Rejects
    /// <c>/model</c> lines whose argument is missing or not a recognized
    /// model ID, which would otherwise surface as a session-level error after
    /// already clearing the editor.
    /// </summary>
    public bool TryValidateForSubmission(string text)
    {
        if (!text.StartsWith("/model", StringComparison.OrdinalIgnoreCase))
            return true;

        var parts = text[1..].Split(' ', 2);
        var arg = parts.Length > 1 ? parts[1].Trim() : "";
        return arg.Length > 0 && _validModelIds.Contains(arg, StringComparer.OrdinalIgnoreCase);
    }

    public CommandOutcome Dispatch(string text)
    {
        // Plain text — run it as a normal turn.
        if (!text.StartsWith('/'))
            return new CommandOutcome.StartTurn(text);

        var parts = text[1..].Split(' ', 2);
        var command = parts[0].ToLowerInvariant();
        var args = parts.Length > 1 ? parts[1] : null;

        // /quit is a TUI exit concern — handled here rather than delegating to
        // the agent layer, because only the TUI knows how to tear itself down.
        if (command == "quit")
            return new CommandOutcome.Exit();

        // /connect opens the provider/key/model wizard.
        if (command == "connect")
            return new CommandOutcome.OpenWizard();

        // Delegate built-in commands to the session layer, which owns the
        // configuration and knows what each command does. A non-null result
        // means the session recognized it and has already executed.
        var session = sessionSource();
        var result = session?.ExecuteBuiltInCommand(command, args);
        if (result is not null)
        {
            if (result.IsError)
                return new CommandOutcome.Unknown($"{command}: {result.Message}");

            // /model invalidates the context-window cache so the status line
            // picks up the new model's window size on the next render cycle.
            var invalidated = command == "model";
            return new CommandOutcome.Handled(invalidated);
        }

        // Not a built-in — check if it's a user-invocable skill. Skills fall
        // through to the normal turn path: OxSession.RunTurnAsync expands the
        // skill template before sending it to the LLM.
        if (commandRegistry.UserInvocableNames.Contains(command, StringComparer.OrdinalIgnoreCase))
            return new CommandOutcome.StartTurn(text);

        return new CommandOutcome.Unknown($"/{command}");
    }
}
