using dotenv.net;

namespace Ur.Cli;

/// <summary>
/// Shared boot helper used by every CLI command.
///
/// All commands follow the same boot sequence:
///   1. Load environment variables from a .env file (traverses up the directory tree, so the
///      developer can place a .env at the repo root with OPENROUTER_API_KEY etc.).
///   2. Start the UrHost for the current working directory.
///
/// Factoring this here prevents every command from duplicating the startup dance and ensures
/// consistent behaviour when running any subcommand.
/// </summary>
internal static class HostRunner
{
    /// <summary>
    /// Boots the Ur host for the current working directory.
    /// Loads .env files before handing control to <paramref name="action"/>.
    /// Returns the exit code returned by <paramref name="action"/> (0 = success).
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

        var host = await UrHost.StartAsync(Environment.CurrentDirectory, ct: ct);
        return await action(host, ct);
    }
}
