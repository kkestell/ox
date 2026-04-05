using System.Runtime.InteropServices;
using Microsoft.Extensions.AI;
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
    private readonly SessionStore _sessions;
    private readonly Func<string, IChatClient>? _chatClientFactoryOverride;

    // Test-only: additional tools to merge into every session registry. Allows
    // tests to inject fake tools (e.g. a mock write_file) without changing the
    // production code path. Last-write-wins in ToolRegistry means these override builtins.
    private readonly ToolRegistry? _additionalTools;

    public string WorkspacePath => Workspace.RootPath;
    public UrConfiguration Configuration { get; }
    public ExtensionCatalog Extensions { get; }

    /// <summary>
    /// Exposed internally so UrSession can pass the workspace to the AgentLoop,
    /// which uses it to enforce workspace-boundary permission policy at invocation time.
    /// </summary>
    internal Workspace Workspace { get; }

    /// <summary>
    /// The loaded skill registry. Exposed internally so UrSession can access it
    /// for slash command lookup and system prompt building.
    /// </summary>
    internal SkillRegistry Skills { get; }

    private readonly string _userDataDirectory;

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
        Workspace = workspace;
        _sessions = sessions;
        Extensions = extensions;
        Skills = skills;
        _userDataDirectory = userDataDirectory;
        _chatClientFactoryOverride = chatClientFactoryOverride;
        _additionalTools = additionalTools;
        // modelCatalog and settings are used only to construct Configuration and are not stored.
        Configuration = new UrConfiguration(modelCatalog, settings, keyring);
    }

    internal IChatClient CreateChatClient(string modelId)
    {
        if (_chatClientFactoryOverride is not null)
            return _chatClientFactoryOverride(modelId);

        var apiKey = Configuration.GetApiKey()
            ?? throw new InvalidOperationException(
                "No OpenRouter API key configured. Set one with 'ur setup'.");

        return ChatClientFactory.Create(modelId, apiKey);
    }

    /// <summary>
    /// Builds a tool registry using the unified factory pattern. Convenience method
    /// used by tests that need to verify tool registration (e.g. checking that an
    /// extension's tools appear after activation). Production code should prefer the
    /// factory loop in <see cref="Sessions.UrSession.RunTurnAsync"/>, which has a full
    /// <see cref="ToolContext"/> including a live chat client and turn callbacks.
    ///
    /// ChatClient is null here because this is called outside of a turn — no
    /// client is needed to verify that tools are registered. Tools that require
    /// a ChatClient at invocation time (e.g. a future SubagentTool) will throw
    /// at invocation, not at registration.
    /// </summary>
    internal ToolRegistry BuildSessionToolRegistry(string sessionId)
    {
        var registry = new ToolRegistry();
        // Build a minimal ToolContext — sufficient for all current tool factories which
        // only need Workspace and SessionId. Per-turn context (ChatClient, callbacks,
        // system prompt) will be added to ToolContext when a tool that needs them is implemented.
        var context = new ToolContext(Workspace, sessionId);

        // Register builtins via the same factory list used by RunTurnAsync.
        foreach (var (factory, operationType, targetExtractor) in BuiltinToolFactories.All)
        {
            var tool = factory(context);
            if (registry.Get(tool.Name) is null)
                registry.Register(tool, operationType, targetExtractor: targetExtractor);
        }

        // Skill tool, bound to the session ID from the context for variable substitution.
        // Using context.SessionId (rather than the local parameter) keeps the context object
        // the single source of truth for per-session state passed to factories.
        var skillTool = new SkillTool(Skills, context.SessionId);
        if (registry.Get(skillTool.Name) is null)
        {
            registry.Register(
                skillTool,
                ((IToolMeta)skillTool).OperationType,
                targetExtractor: ((IToolMeta)skillTool).TargetExtractor);
        }

        // Extension tools from all active extensions.
        foreach (var (extFactory, extensionId) in Extensions.GetActiveToolFactories())
        {
            var tool = extFactory(context);
            if (registry.Get(tool.Name) is null)
                registry.Register(tool, extensionId: extensionId);
        }

        // Test-injected overrides (last-write-wins).
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

        var workspace = InitializeWorkspace(workspacePath);
        var modelCatalog = InitializeModelCatalog(userDataDirectory);
        var (schemaRegistry, extensionEntries) = await InitializeExtensionsAsync(
            workspace, userDataDirectory, systemExtensionsPath, userExtensionsPath, ct);
        var settings = LoadSettings(
            schemaRegistry, userSettingsPath, workspace, userDataDirectory);
        var extensions = await ActivateExtensionsAsync(
            extensionEntries, workspace, userDataDirectory, ct);
        var skillRegistry = await LoadSkillsAsync(userDataDirectory, workspace, ct);
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

    /// <summary>
    /// Creates and initializes the workspace directory structure.
    /// </summary>
    private static Workspace InitializeWorkspace(string workspacePath)
    {
        var workspace = new Workspace(workspacePath);
        workspace.EnsureDirectories();
        return workspace;
    }

    /// <summary>
    /// Loads the model catalog from the disk cache. No network hit at startup —
    /// the cache is populated lazily when the user first queries available models.
    /// </summary>
    private static ModelCatalog InitializeModelCatalog(string userDataDirectory)
    {
        var cacheDir = Path.Combine(userDataDirectory, "cache");
        var modelCatalog = new ModelCatalog(cacheDir);
        _ = modelCatalog.LoadCache();
        return modelCatalog;
    }

    /// <summary>
    /// Discovers extensions from all three tiers (system, user, workspace) and
    /// registers their settings schemas. Returns both the schema registry (needed
    /// for settings loading) and the extension entries (needed for activation).
    /// </summary>
    private static async Task<(SettingsSchemaRegistry, List<Extension>)> InitializeExtensionsAsync(
        Workspace workspace,
        string userDataDirectory,
        string? systemExtensionsPath,
        string? userExtensionsPath,
        CancellationToken ct)
    {
        var schemaRegistry = new SettingsSchemaRegistry();
        RegisterCoreSchemas(schemaRegistry);

        var discoveredExtensions = await ExtensionLoader.DiscoverAllAsync(
                systemExtensionsPath ?? DefaultSystemExtensionsPath(userDataDirectory),
                userExtensionsPath ?? DefaultUserExtensionsPath(userDataDirectory),
                workspace.ExtensionsDirectory,
                ct)
            .ConfigureAwait(false);

        var extensionEntries = RegisterExtensionSchemas(schemaRegistry, discoveredExtensions);
        return (schemaRegistry, extensionEntries);
    }

    /// <summary>
    /// Loads and validates user + workspace settings against the schema registry.
    /// </summary>
    private static Settings LoadSettings(
        SettingsSchemaRegistry schemaRegistry,
        string? userSettingsPath,
        Workspace workspace,
        string userDataDirectory)
    {
        userSettingsPath ??= DefaultUserSettingsPath(userDataDirectory);
        var loader = new SettingsLoader(schemaRegistry);
        return loader.Load(userSettingsPath, workspace.SettingsPath);
    }

    /// <summary>
    /// Activates discovered extensions by running their Lua entry points and
    /// applying persisted override state (enabled/disabled per extension).
    /// </summary>
    private static async Task<ExtensionCatalog> ActivateExtensionsAsync(
        List<Extension> extensionEntries,
        Workspace workspace,
        string userDataDirectory,
        CancellationToken ct)
    {
        var overrideStore = new ExtensionOverrideStore(userDataDirectory, workspace);
        return await ExtensionCatalog.CreateAsync(extensionEntries, overrideStore, ct)
            .ConfigureAwait(false);
    }

    /// <summary>
    /// Discovers SKILL.md files from user (~/.ur/skills/) and workspace (.ur/skills/)
    /// directories. Skills are a peer subsystem to extensions: both are loaded at
    /// startup, but skills are prompt templates while extensions are Lua code.
    /// </summary>
    private static async Task<SkillRegistry> LoadSkillsAsync(
        string userDataDirectory,
        Workspace workspace,
        CancellationToken ct)
    {
        var userSkillsDir = Path.Combine(userDataDirectory, "skills");
        var loadedSkills = await SkillLoader.LoadAllAsync(
                userSkillsDir, workspace.SkillsDirectory, ct)
            .ConfigureAwait(false);
        return new SkillRegistry(loadedSkills);
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
            callbacks, Workspace.PermissionsPath, DefaultUserPermissionsPath());

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

        var messages = (await SessionStore.ReadAllAsync(session, ct)).ToList();
        return new UrSession(this, session, messages, isPersisted: true, activeModelId: null,
            callbacks, Workspace.PermissionsPath, DefaultUserPermissionsPath());
    }

    internal Task AppendMessageAsync(Session session, ChatMessage message, CancellationToken ct = default) =>
        _sessions.AppendAsync(session, message, ct);

    private static IKeyring CreatePlatformKeyring()
    {
        if (RuntimeInformation.IsOSPlatform(OSPlatform.OSX))
            return new MacOsKeyring();
        return RuntimeInformation.IsOSPlatform(OSPlatform.Linux)
            ? new LinuxKeyring()
            : throw new PlatformNotSupportedException("Ur requires macOS or Linux.");
    }

    private static void RegisterCoreSchemas(SettingsSchemaRegistry registry)
    {
        var stringSchema = System.Text.Json.JsonDocument.Parse("""{"type":"string"}""").RootElement.Clone();

        registry.Register(UrConfiguration.ModelSettingKey, stringSchema);
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
