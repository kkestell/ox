using System.Runtime.InteropServices;
using Microsoft.Extensions.AI;
using Ur.Configuration;
using Ur.Configuration.Keyring;
using Ur.Providers;
using Ur.Sessions;

namespace Ur;

/// <summary>
/// Top-level entry point for the Ur library.
/// Orchestrates startup: workspace → model catalog → schemas → settings → sessions.
/// </summary>
public sealed class UrHost
{
    public Workspace Workspace { get; }
    public ModelCatalog ModelCatalog { get; }
    public SettingsSchemaRegistry SchemaRegistry { get; }
    public Settings Settings { get; }
    public SessionStore Sessions { get; }
    internal IKeyring Keyring { get; }

    private UrHost(
        Workspace workspace,
        ModelCatalog modelCatalog,
        SettingsSchemaRegistry schemaRegistry,
        Settings settings,
        SessionStore sessions,
        IKeyring keyring)
    {
        Workspace = workspace;
        ModelCatalog = modelCatalog;
        SchemaRegistry = schemaRegistry;
        Settings = settings;
        Sessions = sessions;
        Keyring = keyring;
    }

    /// <summary>
    /// Creates an IChatClient for the user's selected model (from settings).
    /// </summary>
    internal IChatClient CreateChatClient()
    {
        var modelId = Settings.Get<string>("ur.model")
            ?? throw new InvalidOperationException("No model selected. Use /model to choose one.");

        return CreateChatClient(modelId);
    }

    /// <summary>
    /// Creates an IChatClient for a specific model.
    /// API key is resolved from the keyring.
    /// </summary>
    internal IChatClient CreateChatClient(string modelId)
    {
        var apiKey = Keyring.GetSecret("ur", "openrouter")
            ?? throw new InvalidOperationException(
                "No OpenRouter API key configured. Set one with 'ur setup'.");

        return ChatClientFactory.Create(modelId, apiKey);
    }

    /// <summary>
    /// Boots the Ur system:
    /// 1. Platform keyring (or injected override)
    /// 2. Workspace setup
    /// 3. Model catalog (load cache)
    /// 4. Schema registration
    /// 5. Settings load/validate
    /// 6. Session store
    /// </summary>
    public static UrHost Start(
        string workspacePath,
        IKeyring? keyring = null,
        string? userSettingsPath = null)
    {
        keyring ??= CreatePlatformKeyring();

        var workspace = new Workspace(workspacePath);
        workspace.EnsureDirectories();

        // Model catalog — load from disk cache (no network hit at startup).
        var cacheDir = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            ".ur", "cache");
        var modelCatalog = new ModelCatalog(cacheDir);
        modelCatalog.LoadCache();

        // Schema registry
        var schemaRegistry = new SettingsSchemaRegistry();
        // TODO: Load extensions (metadata/schemas only)
        RegisterCoreSchemas(schemaRegistry);

        // Load and validate configuration
        userSettingsPath ??= DefaultUserSettingsPath();
        var loader = new SettingsLoader(schemaRegistry);
        var settings = loader.Load(userSettingsPath, workspace.SettingsPath);

        // Session store
        var sessions = new SessionStore(workspace.SessionsDirectory);

        return new UrHost(workspace, modelCatalog, schemaRegistry, settings, sessions, keyring);
    }

    private static IKeyring CreatePlatformKeyring()
    {
        if (RuntimeInformation.IsOSPlatform(OSPlatform.OSX))
            return new MacOSKeyring();
        if (RuntimeInformation.IsOSPlatform(OSPlatform.Linux))
            return new LinuxKeyring();

        throw new PlatformNotSupportedException("Ur requires macOS or Linux.");
    }

    private static void RegisterCoreSchemas(SettingsSchemaRegistry registry)
    {
        var stringSchema = System.Text.Json.JsonDocument.Parse("""{"type":"string"}""").RootElement.Clone();

        registry.Register("ur.model", stringSchema);
    }

    private static string DefaultUserSettingsPath() =>
        Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            ".ur", "settings.json");
}
