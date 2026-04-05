using System.CommandLine;
using Ur.Configuration;

namespace Ur.Cli.Commands;

/// <summary>
/// `ur status` — one-shot health check for the current workspace.
///
/// Boots the host and prints:
///   • Workspace path
///   • Selected model (or "none")
///   • Chat readiness and any blocking issues
///   • Model catalog count
///   • Extension summary (N enabled / M total)
///
/// This is the go-to command for confirming the environment is configured before
/// running chat or integration tasks.
/// </summary>
internal static class StatusCommand
{
    public static Command Build()
    {
        var cmd = new Command("status", "Show workspace configuration and readiness");

        cmd.SetAction(async (_, cancellationToken) =>
            await HostRunner.RunAsync(async (host, _) =>
            {
                var cfg = host.Configuration;
                var readiness = cfg.Readiness;

                Console.WriteLine($"Workspace:  {host.WorkspacePath}");
                Console.WriteLine($"Model:      {cfg.SelectedModelId ?? "(none)"}");

                if (readiness.CanRunTurns)
                {
                    Console.WriteLine("Ready:      yes");
                }
                else
                {
                    Console.WriteLine("Ready:      no");
                    foreach (var issue in readiness.BlockingIssues)
                        Console.WriteLine($"  • {DescribeIssue(issue)}");
                }

                Console.WriteLine($"Catalog:    {cfg.AvailableModels.Count} models available");

                var extensions = host.Extensions.List();
                var enabled = extensions.Count(e => e.DesiredEnabled);
                Console.WriteLine($"Extensions: {enabled} enabled / {extensions.Count} total");

                return 0;
            }, cancellationToken));

        return cmd;
    }

    private static string DescribeIssue(ChatBlockingIssue issue) => issue switch
    {
        ChatBlockingIssue.MissingApiKey         => "No API key configured. Run: ur config set-api-key <key>",
        ChatBlockingIssue.MissingModelSelection => "No model selected. Run: ur config set-model <model-id>",
        _                                       => issue.ToString(),
    };
}
