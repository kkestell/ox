using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Ur.Configuration.Keyring;
using Ur.Hosting;

namespace Ur.Tests.TestSupport;

internal sealed class TempWorkspace : IDisposable
{
    private readonly string? _rootPath;
    private IHost? _host;

    public string WorkspacePath { get; }
    public string UserDataDirectory { get; }
    public string UserSettingsPath { get; }

    /// <summary>
    /// Attaches an <see cref="Microsoft.Extensions.Hosting.IHost"/> to this workspace
    /// so it is disposed when the workspace is disposed. Called by
    /// <see cref="TestHostBuilder"/> to prevent host leaks.
    /// </summary>
    internal void AttachHost(IHost host) => _host = host;

    /// <summary>
    /// Creates a self-contained temp workspace with auto-generated paths.
    /// Dispose deletes everything.
    /// </summary>
    public TempWorkspace()
    {
        _rootPath = Path.Combine(
            Path.GetTempPath(),
            "ur-tests",
            Guid.NewGuid().ToString("N"));

        WorkspacePath = Path.Combine(_rootPath, "workspace");
        UserDataDirectory = Path.Combine(_rootPath, "user-data");
        UserSettingsPath = Path.Combine(UserDataDirectory, "settings.json");

        Directory.CreateDirectory(WorkspacePath);
        Directory.CreateDirectory(UserDataDirectory);
    }

    /// <summary>
    /// Wraps pre-existing paths without owning them. Dispose is a no-op.
    /// Used by tests that manage their own temp directories.
    /// </summary>
    public TempWorkspace(string workspacePath, string userDataDirectory, string userSettingsPath)
    {
        _rootPath = null;
        WorkspacePath = workspacePath;
        UserDataDirectory = userDataDirectory;
        UserSettingsPath = userSettingsPath;
    }

    public void Dispose()
    {
        _host?.Dispose();

        if (_rootPath is not null && Directory.Exists(_rootPath))
            Directory.Delete(_rootPath, recursive: true);
    }
}

internal sealed class TestKeyring : IKeyring
{
    private readonly Dictionary<(string Service, string Account), string> _secrets = new();

    public string? GetSecret(string service, string account) =>
        _secrets.GetValueOrDefault((service, account));

    public void SetSecret(string service, string account, string secret) =>
        _secrets[(service, account)] = secret;

    public void DeleteSecret(string service, string account) =>
        _secrets.Remove((service, account));
}
