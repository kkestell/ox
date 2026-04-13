using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Ur.Configuration;
using Ur.Compaction;
using Ur.Permissions;
using Ur.Settings;
using Ur.Providers;
using Ur.Sessions;
using Ur.Skills;
using Ur.Todo;
using Ur.Tools;

namespace Ur.Hosting;

/// <summary>
/// Top-level entry point for the Ur library.
///
/// Constructed by the DI container via <see cref="ServiceCollectionExtensions.AddUr"/>.
/// All dependencies are injected as constructor parameters — there is no static
/// factory method. The container resolves services in dependency order.
///
/// Per-session and per-turn objects (UrSession, AgentLoop, ToolRegistry) are still
/// constructed procedurally because they need per-call parameters (session ID,
/// chat client, callbacks, etc.).
/// </summary>
public sealed class UrHost
{
    private readonly ISessionStore _sessions;
    private readonly ICompactionStrategy _compactionStrategy;
    private readonly ProviderRegistry _providerRegistry;
    private readonly Func<string, IChatClient>? _chatClientFactoryOverride;

    // Optional context window resolver provided by the host via DI. Ox registers
    // ModelCatalog.ResolveContextWindow as a Func<string, int?> so compaction
    // can check context fill percentage. Null in tests that don't need it.
    private readonly Func<string, int?>? _contextWindowResolver;

    // Test-only: additional tools to merge into every session registry. Allows
    // tests to inject fake tools (e.g. a mock write_file) without changing the
    // production code path. Last-write-wins in ToolRegistry means these override builtins.
    private readonly ToolRegistry? _additionalTools;

    private readonly string _userDataDirectory;
    private readonly ILoggerFactory _loggerFactory;

    public string WorkspacePath => _workspace.RootPath;
    public UrConfiguration Configuration { get; }

    private readonly Workspace _workspace;

    /// <summary>
    /// The loaded skill registry. Exposed for UI consumers (e.g. autocomplete)
    /// that need the full skill list without going through a session.
    /// </summary>
    internal SkillRegistry Skills { get; }

    /// <summary>
    /// The built-in command registry. Exposed for UI consumers (e.g. autocomplete)
    /// that merge built-in commands with skills for prefix matching.
    /// </summary>
    internal BuiltInCommandRegistry BuiltInCommands { get; }

    /// <summary>
    /// The settings schema registry. Exposed internally so tests can register
    /// additional schemas for validation tests without reflection.
    /// </summary>
    internal SettingsSchemaRegistry SettingsSchemas { get; }

    /// <summary>
    /// DI-injectable constructor. All parameters are resolved from the container
    /// registered by <see cref="ServiceCollectionExtensions.AddUr"/>.
    ///
    /// <paramref name="chatClientFactoryOverride"/>, <paramref name="additionalTools"/>,
    /// and <paramref name="contextWindowResolver"/> are optional DI services — null
    /// in production (except contextWindowResolver which Ox registers from ModelCatalog).
    /// </summary>
    internal UrHost(
        Workspace workspace,
        ISessionStore sessions,
        ICompactionStrategy compactionStrategy,
        SkillRegistry skills,
        BuiltInCommandRegistry builtInCommands,
        SettingsSchemaRegistry settingsSchemas,
        UrConfiguration configuration,
        ProviderRegistry providerRegistry,
        ILoggerFactory loggerFactory,
        IOptionsMonitor<UrOptions> optionsMonitor,
        string userDataDirectory,
        Func<string, IChatClient>? chatClientFactoryOverride = null,
        ToolRegistry? additionalTools = null,
        Func<string, int?>? contextWindowResolver = null)
    {
        _workspace = workspace;
        _sessions = sessions;
        _compactionStrategy = compactionStrategy;
        _providerRegistry = providerRegistry;
        Skills = skills;
        BuiltInCommands = builtInCommands;
        SettingsSchemas = settingsSchemas;
        Configuration = configuration;
        _loggerFactory = loggerFactory;
        _userDataDirectory = userDataDirectory;
        _chatClientFactoryOverride = chatClientFactoryOverride;
        _additionalTools = additionalTools;
        _contextWindowResolver = contextWindowResolver;

        // Log startup summary — skills are already loaded by the time
        // the host is constructed (DI resolves them as upstream singletons).
        var logger = loggerFactory.CreateLogger<UrHost>();
        logger.LogInformation(
            "Ur ready: workspace={WorkspacePath}, skills={SkillCount}",
            workspace.RootPath, skills.All().Count);
    }

    internal IChatClient CreateChatClient(string modelId)
    {
        if (_chatClientFactoryOverride is not null)
            return _chatClientFactoryOverride(modelId);

        // Parse the "provider/model" format and dispatch to the registered provider.
        var parsed = ModelId.Parse(modelId);
        var provider = _providerRegistry.Get(parsed.Provider)
            ?? throw new InvalidOperationException(
                $"Unknown provider '{parsed.Provider}'. Known providers: {string.Join(", ", _providerRegistry.ProviderNames)}");

        return provider.CreateChatClient(parsed.Model);
    }

    internal void ConfigureChatOptions(string modelId, ChatOptions options)
    {
        // Chat-client construction and request-shape defaults both belong to the
        // provider layer. Keeping them side by side here means sessions can use
        // test chat-client overrides without losing the real provider's runtime
        // option policy for the selected model ID.
        var parsed = ModelId.Parse(modelId);
        var provider = _providerRegistry.Get(parsed.Provider)
            ?? throw new InvalidOperationException(
                $"Unknown provider '{parsed.Provider}'. Known providers: {string.Join(", ", _providerRegistry.ProviderNames)}");

        provider.ConfigureChatOptions(parsed.Model, options);
    }

    public IReadOnlyList<SessionInfo> ListSessions() =>
        _sessions.List()
            .Select(session => new SessionInfo(session.Id, session.CreatedAt))
            .ToList();

    /// <summary>
    /// Creates a new chat session. The optional <paramref name="callbacks"/> delegate is
    /// captured for the entire session lifetime — the same callback handles all turns.
    /// Pass null to auto-deny all sensitive operations (headless/test use).
    ///
    /// <paramref name="todos"/> optionally injects a pre-existing <see cref="TodoStore"/>
    /// for callers that want to observe plan state outside the session. When null,
    /// the session creates its own store.
    ///
    /// <paramref name="maxIterations"/> caps how many times the AgentLoop's <c>while (true)</c>
    /// can iterate before aborting with a fatal error. Null means no cap. See
    /// <see cref="AgentLoop.AgentLoop"/> for the exact semantics.
    /// </summary>
    public UrSession CreateSession(TurnCallbacks? callbacks = null, TodoStore? todos = null, int? maxIterations = null) =>
        new(Configuration, Skills, BuiltInCommands, _workspace, _loggerFactory,
            _sessions, _compactionStrategy, CreateChatClient, ConfigureChatOptions, _sessions.Create(), [],
            isPersisted: false, activeModelId: null, callbacks,
            _workspace.PermissionsPath, DefaultUserPermissionsPath(),
            _contextWindowResolver, _additionalTools, todos, maxIterations);

    /// <summary>
    /// Opens an existing session by ID. See <see cref="CreateSession"/> for callback semantics.
    /// </summary>
    public async Task<UrSession?> OpenSessionAsync(
        string sessionId,
        TurnCallbacks? callbacks = null,
        CancellationToken ct = default)
    {
        var session = _sessions.GetById(sessionId);
        if (session is null)
            return null;

        var messages = (await _sessions.ReadAllAsync(session, ct)).ToList();
        return new UrSession(Configuration, Skills, BuiltInCommands, _workspace, _loggerFactory,
            _sessions, _compactionStrategy, CreateChatClient, ConfigureChatOptions, session, messages,
            isPersisted: true, activeModelId: null, callbacks,
            _workspace.PermissionsPath, DefaultUserPermissionsPath(),
            _contextWindowResolver, _additionalTools);
    }

    private string DefaultUserPermissionsPath() =>
        Path.Combine(_userDataDirectory, "permissions.jsonl");
}
