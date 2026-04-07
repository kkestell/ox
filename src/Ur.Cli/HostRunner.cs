using dotenv.net;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Ur.Hosting;

namespace Ur.Cli;

/// <summary>
/// Shared boot helper used by every CLI command.
///
/// Builds an <see cref="IHost"/> with all Ur services registered via
/// <see cref="ServiceCollectionExtensions.AddUr"/>. All 6 command files remain
/// unchanged — they still receive <c>(UrHost host, CancellationToken ct)</c>.
///
/// <c>ur --help</c> still skips boot because System.CommandLine handles it
/// before the handler runs.
/// </summary>
internal static class HostRunner
{
    /// <summary>
    /// Boots the Ur host for the current working directory.
    /// Loads .env files, builds the DI container, and hands control to
    /// <paramref name="action"/>. Returns the exit code (0 = success).
    /// </summary>
    public static async Task<int> RunAsync(
        Func<UrHost, CancellationToken, Task<int>> action,
        CancellationToken ct = default)
    {
        // Probe upward from cwd so a repo-root .env is found regardless of where the
        // command is invoked from.  Errors are silenced — missing .env is fine at runtime.
        DotEnv.Load(options: new DotEnvOptions(
            probeForEnv: true,
            probeLevelsToSearch: 8));

        var builder = Host.CreateApplicationBuilder();
        builder.Services.AddUr(new UrStartupOptions
        {
            WorkspacePath = Environment.CurrentDirectory
        });

        using var app = builder.Build();
        await app.StartAsync(ct);

        try
        {
            return await action(app.Services.GetRequiredService<UrHost>(), ct);
        }
        finally
        {
            await app.StopAsync(ct);
        }
    }
}
