using Microsoft.Extensions.Logging;
using Ox.Agent.Configuration;
using Ox.Agent.Permissions;
using Ox.Agent.Settings;
using Ox.Agent.Sessions;
using Ox.Agent.Skills;
using Ox.Agent.Todo;

namespace Ox.Agent.Hosting;

/// <summary>
/// Top-level entry point for the Ox agent layer.
///
/// OxHost is a facade: it owns the workspace-scoped state needed to open
/// sessions (session store, user-data directory, permission-grant store) and
/// a single <see cref="SessionDependencies"/> bundle that carries every shared
/// service every session needs. Per-session or per-turn objects (OxSession,
/// AgentLoop, ToolRegistry) are still constructed procedurally — they need
/// per-call parameters (session ID, chat client, callbacks, etc.).
///
/// The constructor shrank from 14 parameters to the five it actually owns:
/// everything else flows through the session-dependencies record, populated by
/// <c>OxServices.Register</c>.
/// </summary>
public sealed class OxHost
{
    private readonly SessionDependencies _deps;
    private readonly Workspace _workspace;

    public string WorkspacePath => _workspace.RootPath;

    /// <summary>
    /// Application-level configuration (model selection, readiness, keyring
    /// access). Exposed because the TUI and headless runner need it to decide
    /// whether a turn can run and what the current model is.
    /// </summary>
    public OxConfiguration Configuration => _deps.Configuration;

    /// <summary>
    /// The loaded skill registry. Exposed for UI consumers (e.g. autocomplete)
    /// that need the full skill list without going through a session.
    /// </summary>
    internal SkillRegistry Skills => _deps.Skills;

    /// <summary>
    /// The built-in command registry. Exposed for UI consumers (e.g. autocomplete)
    /// that merge built-in commands with skills for prefix matching.
    /// </summary>
    internal BuiltInCommandRegistry BuiltInCommands => _deps.BuiltInCommands;

    /// <summary>
    /// The settings schema registry. Exposed internally so tests can register
    /// additional schemas for validation tests without reflection. It is the
    /// one piece of host state that doesn't belong to any session, so it stays
    /// as a direct constructor parameter rather than riding in the bundle.
    /// </summary>
    internal SettingsSchemaRegistry SettingsSchemas { get; }

    /// <summary>
    /// DI-injectable constructor. All parameters are resolved from the container
    /// registered by <c>OxServices.Register</c>.
    /// </summary>
    internal OxHost(
        SessionDependencies deps,
        Workspace workspace,
        ILoggerFactory loggerFactory,
        SettingsSchemaRegistry settingsSchemas)
    {
        _deps = deps;
        _workspace = workspace;
        SettingsSchemas = settingsSchemas;

        // Log startup summary — skills are already loaded by the time
        // the host is constructed (DI resolves them as upstream singletons).
        var logger = loggerFactory.CreateLogger<OxHost>();
        logger.LogInformation(
            "Ox ready: workspace={WorkspacePath}, skills={SkillCount}",
            workspace.RootPath, deps.Skills.All().Count);
    }

    public IReadOnlyList<SessionInfo> ListSessions() =>
        _deps.Sessions.List()
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
    public OxSession CreateSession(TurnCallbacks? callbacks = null, TodoStore? todos = null, int? maxIterations = null) =>
        new(_deps, _deps.Sessions.Create(), [],
            isPersisted: false, activeModelId: null, callbacks,
            todos, maxIterations);

    /// <summary>
    /// Opens an existing session by ID. See <see cref="CreateSession"/> for callback semantics.
    /// </summary>
    public async Task<OxSession?> OpenSessionAsync(
        string sessionId,
        TurnCallbacks? callbacks = null,
        CancellationToken ct = default)
    {
        var session = _deps.Sessions.GetById(sessionId);
        if (session is null)
            return null;

        var messages = (await _deps.Sessions.ReadAllAsync(session, ct)).ToList();
        return new OxSession(_deps, session, messages,
            isPersisted: true, activeModelId: null, callbacks);
    }

    /// <summary>
    /// Default location for the per-user permission grants file. Kept here so
    /// the path is computed once per host rather than recomputed for each
    /// session that creates a grant store.
    /// </summary>
    internal static string DefaultUserPermissionsPath(string userDataDirectory) =>
        Path.Combine(userDataDirectory, "permissions.jsonl");
}
