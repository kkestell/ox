using Microsoft.Extensions.Hosting;
using Ur.Configuration.Keyring;
using Ur.Providers;

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
/// Writes a providers.json file to the given directory and returns its path.
/// Default content matches the repo's providers.json shape with a minimal
/// set of providers and models suitable for tests.
/// </summary>
internal static class TestProviderConfig
{
    /// <summary>
    /// Minimal providers.json with all five provider types for integration-style
    /// tests that need the full provider registry.
    /// </summary>
    internal const string DefaultJson = """
        {
          "providers": {
            "openai": {
              "type": "openai-compatible",
              "models": [
                { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
              ]
            },
            "google": {
              "type": "google",
              "models": [
                { "name": "Gemini 3.1 Pro", "id": "gemini-3.1-pro-preview", "context_in": 1048576 }
              ]
            },
            "ollama": {
              "type": "ollama",
              "url": "http://localhost:11434",
              "models": [
                { "name": "Qwen3 4B", "id": "qwen3:4b", "context_in": 40960 }
              ]
            },
            "openrouter": {
              "type": "openai-compatible",
              "url": "https://openrouter.ai/api/v1",
              "models": [
                { "name": "Claude 3.5 Sonnet", "id": "anthropic/claude-3.5-sonnet", "context_in": 200000 }
              ]
            },
            "zai-coding": {
              "type": "openai-compatible",
              "url": "https://open.bigmodel.cn/api/paas/v4",
              "models": [
                { "name": "GLM-4.7", "id": "glm-4.7", "context_in": 200000 }
              ]
            }
          }
        }
        """;

    /// <summary>
    /// Writes a providers.json to the given directory and returns the file path.
    /// </summary>
    public static string Write(string directory, string? json = null)
    {
        var path = Path.Combine(directory, "providers.json");
        File.WriteAllText(path, json ?? DefaultJson);
        return path;
    }

    /// <summary>
    /// Creates a <see cref="ProviderConfig"/> from the default test JSON.
    /// Writes to a temp directory that the caller should clean up.
    /// </summary>
    public static ProviderConfig CreateDefault()
    {
        var dir = Path.Combine(Path.GetTempPath(), "ur-tests", Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(dir);
        var path = Write(dir);
        return ProviderConfig.Load(path);
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
