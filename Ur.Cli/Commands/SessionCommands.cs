using System.CommandLine;
using Microsoft.Extensions.AI;
using Ur.Cli;

namespace Ur.Cli.Commands;

/// <summary>
/// `ur sessions *` — inspect persisted chat sessions.
///
/// Subcommands:
///   list                list all sessions in the current workspace (ID, creation time)
///   show &lt;session-id&gt;  print the message history for one session
///
/// Sessions are stored as JSONL files in the workspace's .ur/sessions directory and are
/// immutable once written.  These commands provide read-only access to that history.
/// </summary>
internal static class SessionCommands
{
    public static Command Build()
    {
        var sessions = new Command("sessions", "List and inspect chat sessions");

        sessions.Add(BuildList());
        sessions.Add(BuildShow());

        return sessions;
    }

    // -------------------------------------------------------------------------
    // ur sessions list
    // -------------------------------------------------------------------------

    private static Command BuildList()
    {
        var cmd = new Command("list", "List all sessions in the current workspace");

        cmd.SetAction(async (parseResult, ct) =>
            await HostRunner.RunAsync(async (host, ct) =>
            {
                var sessions = host.ListSessions();

                if (sessions.Count == 0)
                {
                    Console.WriteLine("No sessions found.");
                    return 0;
                }

                Console.WriteLine($"{"ID",-26}  {"Created"}");
                Console.WriteLine(new string('-', 50));

                foreach (var s in sessions)
                    Console.WriteLine($"{s.Id,-26}  {s.CreatedAt:yyyy-MM-dd HH:mm:ss zzz}");

                Console.WriteLine();
                Console.WriteLine($"{sessions.Count} session(s).");
                return 0;
            }, ct));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // ur sessions show <session-id>
    // -------------------------------------------------------------------------

    private static Command BuildShow()
    {
        const int MaxContentLength = 200;

        var idArg = new Argument<string>("session-id")
        {
            Description = "Session ID to display"
        };

        var cmd = new Command("show", "Print the message history for a session");
        cmd.Add(idArg);

        cmd.SetAction(async (parseResult, ct) =>
            await HostRunner.RunAsync(async (host, ct) =>
            {
                var sessionId = parseResult.GetValue(idArg)!;
                var session   = await host.OpenSessionAsync(sessionId, ct);

                if (session is null)
                {
                    Console.Error.WriteLine($"Session not found: {sessionId}");
                    return 1;
                }

                Console.WriteLine($"Session:  {session.Id}");
                Console.WriteLine($"Created:  {session.CreatedAt:yyyy-MM-dd HH:mm:ss zzz}");
                Console.WriteLine($"Messages: {session.Messages.Count}");
                Console.WriteLine(new string('-', 60));

                foreach (var msg in session.Messages)
                {
                    var role    = RoleLabel(msg.Role);
                    var content = ExtractText(msg);

                    // Truncate long content so tool results don't flood the terminal.
                    var display = content.Length > MaxContentLength
                        ? content[..MaxContentLength] + "…"
                        : content;

                    Console.WriteLine($"[{role}] {display}");
                }

                return 0;
            }, ct));

        return cmd;
    }

    // -------------------------------------------------------------------------
    // Shared helpers
    // -------------------------------------------------------------------------

    private static string RoleLabel(ChatRole role)
    {
        if (role == ChatRole.User)      return "user     ";
        if (role == ChatRole.Assistant) return "assistant";
        if (role == ChatRole.Tool)      return "tool     ";
        if (role == ChatRole.System)    return "system   ";
        return role.Value.PadRight(9);
    }

    /// <summary>
    /// Extracts a printable text summary from a <see cref="ChatMessage"/>.
    /// Tool results may contain structured JSON; we emit the raw string so the
    /// operator can see what was exchanged without needing a separate parser.
    /// </summary>
    private static string ExtractText(ChatMessage msg)
    {
        if (msg.Text is { } text)
            return text;

        if (msg.Contents is { Count: > 0 } parts)
        {
            var texts = parts
                .OfType<TextContent>()
                .Select(t => t.Text)
                .Where(t => !string.IsNullOrWhiteSpace(t));
            var joined = string.Join(" ", texts);
            if (!string.IsNullOrWhiteSpace(joined))
                return joined;

            return $"({parts.Count} content part(s))";
        }

        return "(empty)";
    }
}
