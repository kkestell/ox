using System.Runtime.InteropServices;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
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
    private readonly Workspace _workspace;
    private readonly ModelCatalog _modelCatalog;
    private readonly Settings _settings;
    private readonly SessionStore _sessions;
    private readonly IKeyring _keyring;
    private readonly Func<string, IChatClient>? _chatClientFactoryOverride;

    public string WorkspacePath => _workspace.RootPath;
    public UrConfiguration Configuration { get; }

    private UrHost(
        Workspace workspace,
        ModelCatalog modelCatalog,
        Settings settings,
        SessionStore sessions,
        IKeyring keyring,
        Func<string, IChatClient>? chatClientFactoryOverride = null,
        ToolRegistry? tools = null)
    {
        _workspace = workspace;
        _modelCatalog = modelCatalog;
        _settings = settings;
        _sessions = sessions;
        _keyring = keyring;
        _chatClientFactoryOverride = chatClientFactoryOverride;
        Tools = tools ?? new ToolRegistry();
        Configuration = new UrConfiguration(modelCatalog, settings, keyring);
    }

    internal ToolRegistry Tools { get; }

    internal IChatClient CreateChatClient(string modelId)
    {
        if (_chatClientFactoryOverride is not null)
            return _chatClientFactoryOverride(modelId);

        var apiKey = _keyring.GetSecret("ur", "openrouter")
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
        string? userSettingsPath = null) =>
        Start(workspacePath, keyring, userSettingsPath, chatClientFactoryOverride: null, tools: null);

    internal static UrHost Start(
        string workspacePath,
        IKeyring? keyring,
        string? userSettingsPath,
        Func<string, IChatClient>? chatClientFactoryOverride,
        ToolRegistry? tools)
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

        return new UrHost(
            workspace,
            modelCatalog,
            settings,
            sessions,
            keyring,
            chatClientFactoryOverride,
            tools);
    }

    public IReadOnlyList<UrSessionInfo> ListSessions() =>
        _sessions.List()
            .Select(session => new UrSessionInfo(session.Id, session.CreatedAt))
            .ToList();

    public UrSession CreateSession() =>
        new(this, _sessions.Create(), [], isPersisted: false, activeModelId: null);

    public async Task<UrSession?> OpenSessionAsync(
        string sessionId,
        CancellationToken ct = default)
    {
        var session = _sessions.Get(sessionId);
        if (session is null)
            return null;

        var messages = (await _sessions.ReadAllAsync(session, ct)).ToList();
        return new UrSession(this, session, messages, isPersisted: true, activeModelId: null);
    }

    internal Task AppendMessageAsync(Session session, ChatMessage message, CancellationToken ct = default) =>
        _sessions.AppendAsync(session, message, ct);

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
