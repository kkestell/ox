using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging;
using Ox.Agent.Compaction;
using Ox.Agent.Configuration;
using Ox.Agent.Permissions;
using Ox.Agent.Skills;
using Ox.Agent.Tools;

namespace Ox.Agent.Sessions;

/// <summary>
/// Bundle of dependencies shared across every <see cref="OxSession"/> created by
/// a single <see cref="Hosting.OxHost"/>.
///
/// Before this record existed, every session constructor parameter was passed
/// individually — twenty of them — and OxHost held nearly the same list as
/// fields so it could forward them. Collapsing the invariant dependencies into
/// one value makes the session constructor narrow (per-session data only), and
/// means there's a single place to add or remove shared session dependencies.
///
/// Stays <c>internal</c>: callers create sessions through <see cref="Hosting.OxHost"/>,
/// which owns the only instance. The session's public surface
/// (RunTurnAsync, ExecuteBuiltInCommand, Messages, …) is unchanged.
/// </summary>
internal sealed record SessionDependencies(
    OxConfiguration Configuration,
    SkillRegistry Skills,
    BuiltInCommandRegistry BuiltInCommands,
    Workspace Workspace,
    ILoggerFactory LoggerFactory,
    ISessionStore Sessions,
    ICompactionStrategy CompactionStrategy,
    Func<string, IChatClient> ChatClientFactory,
    Action<string, ChatOptions> ConfigureChatOptions,
    Func<string, int?> ResolveContextWindow,
    ToolRegistry? AdditionalTools,
    PermissionGrantStore GrantStore);
