using System.Diagnostics;

namespace Ur.Configuration.Keyring;

/// <summary>
/// macOS keyring implementation using the security CLI (/usr/bin/security).
/// Stores generic passwords in the default (login) keychain.
/// </summary>
public sealed class MacOsKeyring : IKeyring
{
    private const string SecurityPath = "/usr/bin/security";
    private const int ItemNotFound = 44;

    public string? GetSecret(string service, string account)
    {
        var (exitCode, stdout, stderr) = Run(
            "find-generic-password", "-s", service, "-a", account, "-w");

        if (exitCode == ItemNotFound)
            return null;

        if (exitCode != 0)
            throw new InvalidOperationException($"security find-generic-password failed: {stderr}");

        return stdout.TrimEnd('\n', '\r');
    }

    public void SetSecret(string service, string account, string secret)
    {
        // -U = update if exists (upsert)
        var (exitCode, _, stderr) = Run(
            "add-generic-password", "-s", service, "-a", account, "-w", secret, "-U");

        if (exitCode != 0)
            throw new InvalidOperationException($"security add-generic-password failed: {stderr}");
    }

    public void DeleteSecret(string service, string account)
    {
        var (exitCode, _, stderr) = Run(
            "delete-generic-password", "-s", service, "-a", account);

        if (exitCode == ItemNotFound)
            return; // already gone — idempotent

        if (exitCode != 0)
            throw new InvalidOperationException($"security delete-generic-password failed: {stderr}");
    }

    private static (int ExitCode, string Stdout, string Stderr) Run(params string[] args)
    {
        var psi = new ProcessStartInfo
        {
            FileName = SecurityPath,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true,
        };

        foreach (var arg in args)
            psi.ArgumentList.Add(arg);

        using var process = Process.Start(psi)
            ?? throw new InvalidOperationException("Failed to start /usr/bin/security");

        var stdout = process.StandardOutput.ReadToEnd();
        var stderr = process.StandardError.ReadToEnd();
        process.WaitForExit();

        return (process.ExitCode, stdout, stderr);
    }
}
