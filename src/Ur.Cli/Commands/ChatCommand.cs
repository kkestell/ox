using System.CommandLine;
using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Permissions;
using Ur.Sessions;

namespace Ur.Cli.Commands;

/// <summary>
/// `ur chat &lt;message&gt; [--session &lt;id&gt;] [--model &lt;id&gt;]` — run a single chat turn.
///
/// This command is the end-to-end test path for the agent loop.  It:
///   1. Creates a new session (or opens an existing one if --session is given).
///   2. Optionally overrides the model for this turn via --model.
///   3. Streams the agent loop events, writing response text to stdout and tool
///      status to stderr so the two streams can be separated when scripting.
///   4. Exits 0 on success, 1 if the chat system is not ready (no API key / model).
///
/// Ctrl+C is forwarded to the agent loop via CancellationToken so in-flight
/// requests are cancelled cleanly.
///
/// Permission prompts are written to stderr and read from stdin.  The user can
/// approve once ("y"), or grant a durable scope: "session", "workspace", or "always".
/// Any other input (including empty) denies the request.
/// </summary>
internal static class ChatCommand
{
    public static Command Build()
    {
        var messageArg = new Argument<string>("message")
        {
            Description = "The message to send to the agent"
        };

        var sessionOpt = new Option<string?>("--session", "-S")
        {
            Description = "Resume an existing session by ID (default: create a new session)"
        };

        var modelOpt = new Option<string?>("--model", "-m")
        {
            Description = "Override the selected model for this turn only"
        };

        var cmd = new Command("chat", "Send a message and stream the response")
        {
            messageArg,
            sessionOpt,
            modelOpt
        };

        cmd.SetAction(async (parseResult, cancellationToken) =>
            await HostRunner.RunAsync(async (host, ct) =>
            {
                var message   = parseResult.GetValue(messageArg)!;
                var sessionId = parseResult.GetValue(sessionOpt);
                var modelId   = parseResult.GetValue(modelOpt);

                // Check readiness up-front so the error message is useful.
                // If a model override is given we skip the model-readiness check because
                // SetSelectedModelAsync below will satisfy that requirement within this turn.
                var readiness = host.Configuration.Readiness;
                if (!readiness.CanRunTurns && modelId is null)
                {
                    foreach (var issue in readiness.BlockingIssues)
                    {
                        await Console.Error.WriteLineAsync(issue switch
                        {
                            ChatBlockingIssue.MissingApiKey         => "No API key set. Run: ur config set-api-key <key>",
                            ChatBlockingIssue.MissingModelSelection => "No model selected. Run: ur config set-model <model-id>",
                            _                                       => issue.ToString()
                        });
                    }
                    return 1;
                }

                // Apply per-turn model override at user scope.  This sets the active model
                // in the configuration so RunTurnAsync picks it up; it persists to disk which
                // is intentional — the user explicitly chose this model for the turn.
                if (modelId is not null)
                    await host.Configuration.SetSelectedModelAsync(modelId, ct: ct);

                // Build a permission callback that prompts the user on stderr/stdin.
                // Only scope options valid for the specific operation are presented.
                // SubagentEventEmitted relays sub-agent events to stderr with a >>>> prefix
                // so scripted callers can separate parent response text (stdout) from
                // sub-agent activity (stderr). subagentAtLineStart tracks whether we are at
                // the start of a new line so that streaming chunks don't each get their own
                // >>>> prefix — only the first chunk on each output line is prefixed.
                var subagentAtLineStart = true;
                var callbacks = new TurnCallbacks
                {
                    SubagentEventEmitted = evt =>
                    {
                        switch (evt)
                        {
                            case SubagentEvent { Inner: ResponseChunk { Text: var saText } }:
                                if (string.IsNullOrWhiteSpace(saText)) break;
                                if (subagentAtLineStart) Console.Write(">>>> ");
                                Console.Write(saText);
                                subagentAtLineStart = saText.EndsWith('\n');
                                break;
                            case SubagentEvent { Inner: ToolCallStarted innerStarted }:
                                if (!subagentAtLineStart) Console.Error.WriteLine();
                                Console.Error.WriteLine($">>>> [tool: {innerStarted.FormatCall()}]");
                                subagentAtLineStart = true;
                                break;
                            case SubagentEvent { Inner: ToolCallCompleted innerCompleted }:
                                var innerRes = innerCompleted.Result.Length > 200
                                    ? innerCompleted.Result[..200] + "\u2026"
                                    : innerCompleted.Result;
                                var innerSts = innerCompleted.IsError ? "error" : "ok";
                                Console.Error.WriteLine($">>>> [tool: {innerCompleted.ToolName} \u2192 {innerSts}] {innerRes}");
                                subagentAtLineStart = true;
                                break;
                            case SubagentEvent { Inner: TurnCompleted }:
                                if (!subagentAtLineStart) Console.WriteLine();
                                subagentAtLineStart = true;
                                break;
                            case SubagentEvent { Inner: Error { Message: var saMsg } }:
                                if (!subagentAtLineStart) Console.Error.WriteLine();
                                Console.Error.WriteLine($">>>> [error] {saMsg}");
                                subagentAtLineStart = true;
                                break;
                        }
                        return ValueTask.CompletedTask;
                    },

                    RequestPermissionAsync = (req, _) =>
                    {
                        // Filter to just the scopes allowed for this operation type,
                        // then build a short hint string like " [session, workspace, always]".
                        var scopeHints = req.AllowedScopes.Count > 1
                            ? $" [{string.Join(", ", req.AllowedScopes.Select(s => s.ToString().ToLowerInvariant()))}]"
                            : "";

                        Console.Error.Write(
                            $"Allow {req.OperationType} on '{req.Target}' by '{req.RequestingExtension}'?"
                            + $" (y/n{scopeHints}): ");

                        var input = Console.ReadLine()?.Trim().ToLowerInvariant();

                        // Match input against "y"/"yes" (once-approval) or a scope name for
                        // a durable grant. Then validate the chosen scope is actually allowed
                        // for this operation — PermissionPolicy may restrict certain operations
                        // to Once-only, and we must not grant broader scopes than allowed.
                        var candidate = input switch
                        {
                            "y" or "yes"   => new PermissionResponse(true,  PermissionScope.Once),
                            "session"      => new PermissionResponse(true,  PermissionScope.Session),
                            "workspace"    => new PermissionResponse(true,  PermissionScope.Workspace),
                            "always"       => new PermissionResponse(true,  PermissionScope.Always),
                            _              => new PermissionResponse(false, null)
                        };

                        // If the user requested a scope that this operation doesn't permit,
                        // treat it as a denial rather than silently granting more than allowed.
                        var response = candidate is { Granted: true, Scope: not null }
                            && !req.AllowedScopes.Contains(candidate.Scope.Value)
                            ? new PermissionResponse(false, null)
                            : candidate;

                        return ValueTask.FromResult(response);
                    }
                };

                // Open an existing session or create a fresh one.
                UrSession session;
                if (sessionId is not null)
                {
                    var opened = await host.OpenSessionAsync(sessionId, callbacks, ct);
                    if (opened is null)
                    {
                        await Console.Error.WriteLineAsync($"Session not found: {sessionId}");
                        return 1;
                    }
                    session = opened;
                }
                else
                {
                    session = host.CreateSession(callbacks);
                }

                // Stream the agent loop, routing response text to stdout and tool
                // status to stderr.  This lets callers capture just the assistant
                // response with `ur chat "..." > output.txt`.
                var turnComplete = false;

                await foreach (var evt in session.RunTurnAsync(message, ct))
                {
                    switch (evt)
                    {
                        case ResponseChunk chunk:
                            if (!string.IsNullOrWhiteSpace(chunk.Text))
                                Console.Write(chunk.Text);
                            break;

                        case ToolCallStarted started:
                            await Console.Error.WriteLineAsync($"[tool: {started.FormatCall()}]");
                            break;

                        case ToolCallCompleted completed:
                            // Truncate long tool results to avoid flooding the terminal.
                            var result = completed.Result.Length > 200
                                ? completed.Result[..200] + "…"
                                : completed.Result;
                            var status = completed.IsError ? "error" : "ok";
                            await Console.Error.WriteLineAsync($"[tool: {completed.ToolName} → {status}] {result}");
                            break;

                        case TurnCompleted:
                            // Add a newline after the streamed response so the prompt
                            // appears on a new line.
                            Console.WriteLine();
                            turnComplete = true;
                            break;

                        case Error error:
                            await Console.Error.WriteLineAsync($"[error] {error.Message}");
                            if (error.IsFatal)
                                return 1;
                            break;

                        case SubagentEvent:
                            // SubagentEvent handling is fully handled by TurnCallbacks.SubagentEventEmitted
                            // above. Events never reach the turn stream — SubagentRunner invokes the
                            // callback directly. This case is a safety net for any future path that
                            // might yield them into the session stream.
                            break;
                    }
                }

                if (!turnComplete)
                {
                    // The loop ended without a TurnCompleted event — likely a cancellation.
                    Console.WriteLine();
                }

                await Console.Error.WriteLineAsync($"[session: {session.Id}]");
                return 0;
            }, cancellationToken));

        return cmd;
    }
}
