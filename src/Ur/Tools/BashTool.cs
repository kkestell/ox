using System.Diagnostics;
using System.Text.Json;
using Microsoft.Extensions.AI;

namespace Ur.Tools;

/// <summary>
/// Built-in tool that executes a shell command in the workspace directory.
/// Runs commands via /bin/bash -c, captures stdout and stderr, and enforces
/// a configurable timeout. This is the only tool that introduces arbitrary
/// process execution — the permission system gates it behind per-invocation
/// user approval (ExecuteCommand with Once-only scope).
/// </summary>
internal sealed class BashTool(Workspace workspace) : AIFunction
{
    private const int DefaultTimeoutMs = 120_000; // 2 minutes

    private static readonly JsonElement Schema = JsonDocument.Parse("""
        {
            "type": "object",
            "properties": {
                "command": {
                    "type": "string",
                    "description": "The shell command to execute."
                },
                "timeout_ms": {
                    "type": "integer",
                    "description": "Timeout in milliseconds. Defaults to 120000 (2 minutes)."
                }
            },
            "required": ["command"],
            "additionalProperties": false
        }
        """).RootElement.Clone();

    public override string Name => "bash";
    public override string Description => "Execute a shell command in the workspace directory.";
    public override JsonElement JsonSchema => Schema;

    protected override async ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var command = ToolArgHelpers.GetRequiredString(arguments, "command");
        var timeoutMs = ToolArgHelpers.GetOptionalInt(arguments, "timeout_ms") ?? DefaultTimeoutMs;

        var psi = new ProcessStartInfo
        {
            FileName = "/bin/bash",
            WorkingDirectory = workspace.RootPath,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true
        };
        psi.ArgumentList.Add("-c");
        psi.ArgumentList.Add(command);

        using var process = Process.Start(psi);
        if (process is null)
            return "Failed to start process.";

        using var cts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        cts.CancelAfter(TimeSpan.FromMilliseconds(timeoutMs));

        var timedOut = false;

        // Read stdout and stderr concurrently via Task.WhenAll to avoid
        // deadlocks — sequential awaits can block if the process fills one
        // pipe buffer while we're waiting on the other.
        var stdoutTask = process.StandardOutput.ReadToEndAsync(cts.Token);
        var stderrTask = process.StandardError.ReadToEndAsync(cts.Token);

        try
        {
            await Task.WhenAll(stdoutTask, stderrTask);
            await process.WaitForExitAsync(cts.Token);
        }
        catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            // Timeout hit — kill the process tree and recover whatever partial
            // output completed before cancellation.
            process.Kill(entireProcessTree: true);
            timedOut = true;
        }

        // Recover whatever output completed, even on timeout.
        var stdout = stdoutTask.IsCompletedSuccessfully ? stdoutTask.Result : "";
        var stderr = stderrTask.IsCompletedSuccessfully ? stderrTask.Result : "";

        // After a kill, ExitCode may not be available immediately — wait
        // briefly for the OS to reap the process before reading exit code.
        var exitCode = -1;
        if (!timedOut)
        {
            exitCode = process.ExitCode;
        }
        else
        {
            try
            {
                process.WaitForExit(3000);
                exitCode = process.ExitCode;
            }
            catch { /* Process may not have fully exited yet — use -1 */ }
        }

        return FormatResult(exitCode, stdout, stderr, timedOut);
    }

    private static string FormatResult(int exitCode, string stdout, string stderr, bool timedOut)
    {
        var parts = new List<string>();

        if (timedOut)
            parts.Add("[command timed out]");

        parts.Add($"Exit code: {exitCode}");

        if (!string.IsNullOrEmpty(stdout))
        {
            parts.Add("--- stdout ---");
            parts.Add(ToolArgHelpers.TruncateOutput(stdout));
        }

        if (!string.IsNullOrEmpty(stderr))
        {
            parts.Add("--- stderr ---");
            parts.Add(ToolArgHelpers.TruncateOutput(stderr));
        }

        // If the process produced no output at all, say so explicitly
        // so the LLM doesn't think it missed something.
        if (string.IsNullOrEmpty(stdout) && string.IsNullOrEmpty(stderr) && !timedOut)
            parts.Add("(no output)");

        return string.Join('\n', parts);
    }
}
