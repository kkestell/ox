using Microsoft.Extensions.AI;
using Ur.Configuration;
using Ur.Providers;
using Ur.Sessions;

namespace Ur;

/// <summary>
/// Top-level entry point for the Ur library.
/// Orchestrates startup: workspace → provider registry → schemas → settings → sessions.
/// </summary>
public sealed class UrHost
{
    public Workspace Workspace { get; }
    public ProviderRegistry ProviderRegistry { get; }
    public SettingsSchemaRegistry SchemaRegistry { get; }
    public Settings Settings { get; }
    public SessionStore Sessions { get; }
    public IChatClientFactory ChatClientFactory { get; }

    private UrHost(
        Workspace workspace,
        ProviderRegistry providerRegistry,
        SettingsSchemaRegistry schemaRegistry,
        Settings settings,
        SessionStore sessions,
        IChatClientFactory chatClientFactory)
    {
        Workspace = workspace;
        ProviderRegistry = providerRegistry;
        SchemaRegistry = schemaRegistry;
        Settings = settings;
        Sessions = sessions;
        ChatClientFactory = chatClientFactory;
    }

    /// <summary>
    /// Creates an IChatClient for the default model (from settings).
    /// </summary>
    public IChatClient CreateChatClient(string apiKey)
    {
        var modelId = Settings.Get<string>("ur.defaultModel")
            ?? throw new InvalidOperationException("No default model configured. Set 'ur.defaultModel' in settings.");

        var model = ProviderRegistry.GetModel(modelId)
            ?? throw new InvalidOperationException($"Unknown model '{modelId}'. Check provider registry.");

        return ChatClientFactory.Create(model.ProviderId, model.Id, apiKey);
    }

    /// <summary>
    /// Creates an IChatClient for a specific model.
    /// </summary>
    public IChatClient CreateChatClient(string modelId, string apiKey)
    {
        var model = ProviderRegistry.GetModel(modelId)
            ?? throw new InvalidOperationException($"Unknown model '{modelId}'. Check provider registry.");

        return ChatClientFactory.Create(model.ProviderId, model.Id, apiKey);
    }

    /// <summary>
    /// Boots the Ur system with the two-phase startup:
    /// Phase 1: Load provider registry + extension metadata (schemas only).
    /// Phase 2: Validate and load configuration.
    /// </summary>
    public static UrHost Start(
        string workspacePath,
        IChatClientFactory chatClientFactory,
        string? userSettingsPath = null)
    {
        var workspace = new Workspace(workspacePath);
        workspace.EnsureDirectories();

        // Phase 1: Build the schema registry
        var schemaRegistry = new SettingsSchemaRegistry();
        var providerRegistry = new ProviderRegistry();

        // TODO: Load embedded provider registry data
        // TODO: Load extensions (metadata/schemas only)

        // Register provider model settings schemas
        providerRegistry.RegisterSettingsSchemas(schemaRegistry);

        // Register core settings schemas
        RegisterCoreSchemas(schemaRegistry);

        // Phase 2: Load and validate configuration
        userSettingsPath ??= DefaultUserSettingsPath();
        var loader = new SettingsLoader(schemaRegistry);
        var settings = loader.Load(userSettingsPath, workspace.SettingsPath);

        // Phase 3: Create session store
        var sessions = new SessionStore(workspace.SessionsDirectory);

        return new UrHost(workspace, providerRegistry, schemaRegistry, settings, sessions, chatClientFactory);
    }

    private static void RegisterCoreSchemas(SettingsSchemaRegistry registry)
    {
        var stringSchema = System.Text.Json.JsonDocument.Parse("""{"type":"string"}""").RootElement.Clone();

        registry.Register("ur.defaultModel", stringSchema);
    }

    private static string DefaultUserSettingsPath() =>
        Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            ".ur", "settings.json");
}
