using System.Runtime.InteropServices;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Configuration.Keyring;
using Ur.Extensions;
using Ur.Permissions;
using Ur.Providers;
using Ur.Sessions;
using Ur.Skills;
using Ur.Tools;

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
    private readonly string _userDataDirectory;

    // Test-only: additional tools to merge into every session registry. Allows
    // tests to inject fake tools (e.g. a mock write_file) without changing the
    // production code path. Last-write-wins in ToolRegistry means these override builtins.
    private readonly ToolRegistry? _additionalTools;

    public string WorkspacePath => _workspace.RootPath;
    public UrConfiguration Configuration { get; }
    public ExtensionCatalog Extensions { get; }

    /// <summary>
    /// The loaded skill registry. Exposed internally so UrSession can access it
    /// for slash command lookup and system prompt building.
    /// </summary>
    internal SkillRegistry Skills { get; }

    private UrHost(
        Workspace workspace,
        ModelCatalog modelCatalog,
        Settings settings,
        SessionStore sessions,
        ExtensionCatalog extensions,
        SkillRegistry skills,
        IKeyring keyring,
        string userDataDirectory,
        Func<string, IChatClient>? chatClientFactoryOverride = null,
        ToolRegistry? additionalTools = null)
    {
        _workspace = workspace;
        _modelCatalog = modelCatalog;
        _settings = settings;
        _sessions = sessions;
        Extensions = extensions;
        Skills = skills;
        _keyring = keyring;
        _userDataDirectory = userDataDirectory;
        _chatClientFactoryOverride = chatClientFactoryOverride;
        _additionalTools = additionalTools;
        Configuration = new UrConfiguration(modelCatalog, settings, keyring);
    }

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
    /// Builds a fresh tool registry for a session. Each session gets its own
    /// snapshot of the current tool state: built-in tools, active extension tools,
    /// and the skill tool (bound to this session's ID for variable substitution).
    ///
    /// This per-session approach eliminates shared mutable state — enabling or
    /// disabling an extension mid-conversation takes effect on the next session.
    /// </summary>
    internal ToolRegistry BuildSessionToolRegistry(string sessionId)
    {
        var registry = new ToolRegistry();

        // Built-in file, search, and shell tools.
        BuiltinTools.RegisterAll(registry, _workspace);

        // Skill invocation tool, bound to this session's ID.
        BuiltinTools.RegisterSkillTool(registry, Skills, sessionId);

        // Tools from active extensions (Lua-defined).
        Extensions.RegisterActiveToolsInto(registry);

        // Test-injected tools override anything above (last-write-wins).
        _additionalTools?.MergeInto(registry);

        return registry;
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
    public static Task<UrHost> StartAsync(
        string workspacePath,
        IKeyring? keyring = null,
        string? userSettingsPath = null,
        CancellationToken ct = default) =>
        StartAsync(
            workspacePath,
            keyring,
            userSettingsPath,
            chatClientFactoryOverride: null,
            additionalTools: null,
            systemExtensionsPath: null,
            userExtensionsPath: null,
            userDataDirectory: null,
            ct);

    internal static async Task<UrHost> StartAsync(
        string workspacePath,
        IKeyring? keyring,
        string? userSettingsPath,
        Func<string, IChatClient>? chatClientFactoryOverride,
        ToolRegistry? additionalTools = null,
        string? systemExtensionsPath = null,
        string? userExtensionsPath = null,
        string? userDataDirectory = null,
        CancellationToken ct = default)
    {
        keyring ??= CreatePlatformKeyring();
        userDataDirectory ??= DefaultUserDataDirectory();

        var workspace = new Workspace(workspacePath);
        workspace.EnsureDirectories();

        // Model catalog — load from disk cache (no network hit at startup).
        var cacheDir = Path.Combine(userDataDirectory, "cache");
        var modelCatalog = new ModelCatalog(cacheDir);
        modelCatalog.LoadCache();

        // Schema registry
        var schemaRegistry = new SettingsSchemaRegistry();
        RegisterCoreSchemas(schemaRegistry);
        var discoveredExtensions = await ExtensionLoader.DiscoverAllAsync(
                systemExtensionsPath ?? DefaultSystemExtensionsPath(userDataDirectory),
                userExtensionsPath ?? DefaultUserExtensionsPath(userDataDirectory),
                workspace.ExtensionsDirectory,
                ct)
            .ConfigureAwait(false);
        var extensionEntries = RegisterExtensionSchemas(schemaRegistry, discoveredExtensions);

        // Load and validate configuration
        userSettingsPath ??= DefaultUserSettingsPath(userDataDirectory);
        var loader = new SettingsLoader(schemaRegistry);
        var settings = loader.Load(userSettingsPath, workspace.SettingsPath);

        var overrideStore = new ExtensionOverrideStore(userDataDirectory, workspace);
        var extensions = await ExtensionCatalog.CreateAsync(
                extensionEntries,
                overrideStore,
                ct)
            .ConfigureAwait(false);

        // Skill loading — discover SKILL.md files from user (~/.ur/skills/) and
        // workspace (.ur/skills/) directories. Skills are a peer subsystem to
        // extensions: both are loaded at startup, but skills are prompt templates
        // while extensions are Lua code registering tools.
        var userSkillsDir = Path.Combine(userDataDirectory, "skills");
        var loadedSkills = await SkillLoader.LoadAllAsync(
                userSkillsDir, workspace.SkillsDirectory, ct)
            .ConfigureAwait(false);
        var skillRegistry = new SkillRegistry(loadedSkills);

        // Session store
        var sessions = new SessionStore(workspace.SessionsDirectory);

        return new UrHost(
            workspace,
            modelCatalog,
            settings,
            sessions,
            extensions,
            skillRegistry,
            keyring,
            userDataDirectory,
            chatClientFactoryOverride,
            additionalTools);
    }

    public IReadOnlyList<SessionInfo> ListSessions() =>
        _sessions.List()
            .Select(session => new SessionInfo(session.Id, session.CreatedAt))
            .ToList();

    /// <summary>
    /// Creates a new chat session. The optional <paramref name="callbacks"/> delegate is
    /// captured for the entire session lifetime — the same callback handles all turns.
    /// Pass null to auto-deny all sensitive operations (headless/test use).
    /// </summary>
    public UrSession CreateSession(TurnCallbacks? callbacks = null) =>
        new(this, _sessions.Create(), [], isPersisted: false, activeModelId: null,
            callbacks, _workspace.PermissionsPath, DefaultUserPermissionsPath());

    /// <summary>
    /// Opens an existing session by ID. See <see cref="CreateSession"/> for callback semantics.
    /// </summary>
    public async Task<UrSession?> OpenSessionAsync(
        string sessionId,
        TurnCallbacks? callbacks = null,
        CancellationToken ct = default)
    {
        var session = _sessions.Get(sessionId);
        if (session is null)
            return null;

        var messages = (await _sessions.ReadAllAsync(session, ct)).ToList();
        return new UrSession(this, session, messages, isPersisted: true, activeModelId: null,
            callbacks, _workspace.PermissionsPath, DefaultUserPermissionsPath());
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

    private static List<Extension> RegisterExtensionSchemas(
        SettingsSchemaRegistry registry,
        IEnumerable<Extension> discoveredExtensions)
    {
        var extensions = new List<Extension>();

        foreach (var extension in discoveredExtensions)
        {
            try
            {
                var duplicateKey = extension.SettingsSchemas.Keys
                    .FirstOrDefault(registry.IsKnown);
                if (duplicateKey is not null)
                {
                    throw new InvalidOperationException(
                        $"Settings key '{duplicateKey}' is already registered.");
                }

                foreach (var (key, schema) in extension.SettingsSchemas)
                    registry.Register(key, schema);

                extensions.Add(extension);
            }
            catch (InvalidOperationException ex)
            {
                Console.Error.WriteLine(
                    $"Extension '{extension.Name}' skipped: failed to register settings schemas: {ex.Message}");
            }
        }

        return extensions;
    }

    private static string DefaultUserDataDirectory() =>
        Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            ".ur");

    private static string DefaultSystemExtensionsPath(string userDataDirectory) =>
        Path.Combine(userDataDirectory, "extensions", "system");

    private static string DefaultUserExtensionsPath(string userDataDirectory) =>
        Path.Combine(userDataDirectory, "extensions", "user");

    private static string DefaultUserSettingsPath(string userDataDirectory) =>
        Path.Combine(userDataDirectory, "settings.json");

    private string DefaultUserPermissionsPath() =>
        Path.Combine(_userDataDirectory, "permissions.jsonl");
}
