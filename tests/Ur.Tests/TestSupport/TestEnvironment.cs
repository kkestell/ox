using Microsoft.Extensions.Hosting;
using Ur.Configuration.Keyring;

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

/// <summary>
/// Creates <see cref="ModelCatalog"/> instances for tests. The empty variant
/// is for tests that construct providers but don't need real catalog data.
/// The populated variant writes a minimal cache file so <see cref="Providers.ModelCatalog.LoadCache"/>
/// returns real model entries.
/// </summary>
internal static class TestCatalog
{
    public static Providers.ModelCatalog CreateEmpty() =>
        new(Path.Combine(Path.GetTempPath(), "ur-tests", Guid.NewGuid().ToString("N")));

    /// <summary>
    /// Creates a catalog pre-populated with the given model entries.
    /// Writes a cache file in the OpenRouter API format and calls LoadCache().
    /// </summary>
    public static Providers.ModelCatalog CreateWithModels(params (string id, int contextLength)[] models)
    {
        var cacheDir = Path.Combine(Path.GetTempPath(), "ur-tests", Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(cacheDir);

        // Build a minimal JSON array matching the OpenRouter API shape.
        // Architecture must be non-null or LoadCache() rejects the cache as stale.
        var entries = models.Select(m =>
            $$$"""{"id":"{{{m.id}}}","name":"{{{m.id}}}","context_length":{{{m.contextLength}}},"architecture":{"modality":"text"}}""");
        var json = $"[{string.Join(",", entries)}]";
        File.WriteAllText(Path.Combine(cacheDir, "models.json"), json);

        var catalog = new Providers.ModelCatalog(cacheDir);
        catalog.LoadCache();
        return catalog;
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
