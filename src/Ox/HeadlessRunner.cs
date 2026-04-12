using Ur.AgentLoop;
using Ur.Hosting;
using Ur.Permissions;

namespace Ox;

/// <summary>
/// Drives the Ur agent loop without a TUI — reads turns from the CLI args,
/// writes LLM responses to stdout, and exits. Sits at the same level as
/// <see cref="OxApp"/> in the Ox architecture: it uses <see cref="UrHost"/>
/// to create a session and run turns, but never touches any TUI code.
///
/// Metrics are not collected here — <see cref="Ur.Sessions.UrSession"/> accumulates
/// them during RunTurnAsync and writes the metrics JSON file on DisposeAsync.
/// HeadlessRunner's only job is to feed turns and stream output.
/// </summary>
internal sealed class HeadlessRunner(UrHost host, IReadOnlyList<string> turns, bool yolo)
{
    /// <summary>
    /// Runs all turns sequentially against a single session.
    /// Returns 0 on success, 1 on fatal error.
    /// </summary>
    public async Task<int> RunAsync(CancellationToken ct)
    {
        // YOLO mode: auto-grant all permission requests at session scope so the
        // agent can execute tools without interactive prompts. When not YOLO,
        // callbacks are null which means auto-deny (safe dry-run).
        var callbacks = yolo
            ? new TurnCallbacks
            {
                RequestPermissionAsync = (_, _) =>
                    ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Session)),
            }
            : null;

        await using var session = host.CreateSession(callbacks);

        foreach (var turn in turns)
        {
            var hadFatalError = false;

            await foreach (var evt in session.RunTurnAsync(turn, ct))
            {
                switch (evt)
                {
                    case ResponseChunk { Text: var text }:
                        Console.Write(text);
                        break;

                    case TurnError { IsFatal: true, Message: var msg }:
                        await Console.Error.WriteLineAsync(msg);
                        hadFatalError = true;
                        break;

                    case TurnError { Message: var msg }:
                        await Console.Error.WriteLineAsync($"Warning: {msg}");
                        break;
                }
            }

            // Ensure each turn's output ends on a new line.
            Console.WriteLine();

            if (hadFatalError)
                return 1;
        }

        return 0;
    }
}
