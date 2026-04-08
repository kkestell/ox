using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging;
using Ur.Configuration;
using Ur.Extensions;
using Ur.Hosting;
using Ur.Permissions;
using Ur.Providers;
using Ur.Sessions;
using Ur.Skills;
using Ur.Todo;
using Ur.Tools;

namespace Ur;

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
    private readonly SessionStore _sessions;
    private readonly ProviderRegistry _providerRegistry;
    private readonly Func<string, IChatClient>? _chatClientFactoryOverride;

    // Test-only: additional tools to merge into every session registry. Allows
    // tests to inject fake tools (e.g. a mock write_file) without changing the
    // production code path. Last-write-wins in ToolRegistry means these override builtins.
    private readonly ToolRegistry? _additionalTools;

    private readonly string _userDataDirectory;
    private readonly ILoggerFactory _loggerFactory;

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

    /// <summary>
    /// The built-in command registry. Exposed internally so UrSession can intercept
    /// built-in commands before they reach the LLM.
    /// </summary>
    internal BuiltInCommandRegistry BuiltInCommands { get; }

    /// <summary>
    /// The settings schema registry. Exposed internally so tests can register
    /// additional schemas for validation tests without reflection.
    /// </summary>
    internal SettingsSchemaRegistry SettingsSchemas { get; }

    /// <summary>
    /// Logger factory passed to per-turn objects (AgentLoop, SubagentRunner) that
    /// can't be DI-injected because they're created procedurally with per-call parameters.
    /// </summary>
    internal ILoggerFactory LoggerFactory => _loggerFactory;

    /// <summary>
    /// DI-injectable constructor. All parameters are resolved from the container
    /// registered by <see cref="ServiceCollectionExtensions.AddUr"/>.
    /// </summary>
    internal UrHost(
        Workspace workspace,
        SessionStore sessions,
        ExtensionCatalog extensions,
        SkillRegistry skills,
        BuiltInCommandRegistry builtInCommands,
        SettingsSchemaRegistry settingsSchemas,
        UrConfiguration configuration,
        ProviderRegistry providerRegistry,
        ILoggerFactory loggerFactory,
        UrStartupOptions options)
    {
        Workspace = workspace;
        _sessions = sessions;
        _providerRegistry = providerRegistry;
        Extensions = extensions;
        Skills = skills;
        BuiltInCommands = builtInCommands;
        SettingsSchemas = settingsSchemas;
        Configuration = configuration;
        _loggerFactory = loggerFactory;
        _userDataDirectory = options.UserDataDirectory
            ?? ServiceCollectionExtensions.DefaultUserDataDirectory();
        _chatClientFactoryOverride = options.ChatClientFactoryOverride;
        _additionalTools = options.AdditionalTools;

        // Log startup summary — extensions and skills are already loaded by the time
        // the host is constructed (DI resolves them as upstream singletons).
        var logger = loggerFactory.CreateLogger<UrHost>();
        logger.LogInformation(
            "Ur ready: workspace={WorkspacePath}, extensions={ExtensionCount}, skills={SkillCount}",
            workspace.RootPath, extensions.List().Count, skills.All().Count);
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
    internal ToolRegistry BuildSessionToolRegistry(string sessionId, TodoStore? todos = null)
    {
        var registry = new ToolRegistry();
        // Build a ToolContext carrying everything tool factories need. The TodoStore
        // is nullable because host-level callers (tests, extensions) may not have a
        // session — TodoWriteTool degrades gracefully when it's null.
        var context = new ToolContext(Workspace, sessionId, todos);

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
    /// so the TUI can bind its sidebar before the session is created (breaking the
    /// sidebar → session → callbacks → viewport circularity). When null, the session
    /// creates its own store.
    /// </summary>
    public UrSession CreateSession(TurnCallbacks? callbacks = null, TodoStore? todos = null) =>
        new(this, _sessions.Create(), [], isPersisted: false, activeModelId: null,
            callbacks, Workspace.PermissionsPath, DefaultUserPermissionsPath(), todos);

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
            callbacks, Workspace.PermissionsPath, DefaultUserPermissionsPath());
    }

    internal Task AppendMessageAsync(Session session, ChatMessage message, CancellationToken ct = default) =>
        _sessions.AppendAsync(session, message, ct);

    private string DefaultUserPermissionsPath() =>
        Path.Combine(_userDataDirectory, "permissions.jsonl");
}
