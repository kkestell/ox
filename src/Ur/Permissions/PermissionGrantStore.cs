using System.Text.Json;
using System.Text.Json.Serialization;

namespace Ur.Permissions;

/// <summary>
/// Stores and checks permission grants across three lifetimes:
///
///   Session   — in-memory only; lost when the process exits.
///   Workspace — persisted to {workspace}/.ur/permissions.jsonl.
///   Always    — persisted to {userDataDir}/permissions.jsonl.
///
/// The store is loaded lazily: workspace and always grants are read from disk on
/// the first IsCovered check, not at construction, so startup is not delayed by
/// disk I/O.
///
/// <b>Lazy-load trade-off:</b> <see cref="IsCovered"/> performs synchronous file I/O
/// on the first call (via <c>EnsureWorkspaceGrantsLoaded</c> / <c>EnsureAlwaysGrantsLoaded</c>).
/// An async factory would be cleaner, but <see cref="PermissionGrantStore"/> is created inside
/// <c>UrSession</c>'s constructor (called synchronously by <c>UrHost.CreateSession</c>),
/// and making that path async would cascade through the session creation API. The lazy
/// approach keeps the public API simple at the cost of a single synchronous file read
/// on the first permission check — acceptable because the files are small (a few KB
/// of JSONL) and the read happens exactly once per session.
///
/// Grant checking uses prefix matching: a grant whose TargetPrefix is a prefix of
/// the requested target covers that request. An empty prefix covers everything.
/// </summary>
internal sealed class PermissionGrantStore
{
    private readonly string _workspacePermissionsPath;
    private readonly string _alwaysPermissionsPath;

    // In-memory grants for the three scopes. Session grants live only here.
    // Workspace and Always lists are populated on first access (lazy load).
    private readonly List<PermissionGrant> _sessionGrants = [];
    private List<PermissionGrant>? _workspaceGrants;
    private List<PermissionGrant>? _alwaysGrants;

    // Source-gen context with AoT-safe generic enum converters for human-readable
    // JSONL output (e.g. "write" instead of 1).
    //
    // NOTE: The enum values changed in the OperationType simplification (Read/Write/Execute
    // replaced ReadInWorkspace/WriteInWorkspace/etc.). Grants persisted before that change
    // will fail to deserialize and be silently skipped — users will be re-prompted.
    // This is intentional: the project is early-stage and backward compat for grant files
    // is not yet a constraint.
    private static readonly PermissionGrantJsonContext JsonContext = new(new JsonSerializerOptions
    {
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
        Converters =
        {
            new JsonStringEnumConverter<OperationType>(JsonNamingPolicy.CamelCase),
            new JsonStringEnumConverter<PermissionScope>(JsonNamingPolicy.CamelCase)
        }
    });

    internal PermissionGrantStore(string workspacePermissionsPath, string alwaysPermissionsPath)
    {
        _workspacePermissionsPath = workspacePermissionsPath;
        _alwaysPermissionsPath = alwaysPermissionsPath;
    }

    /// <summary>
    /// Returns true if any active grant covers the given request.
    /// A grant "covers" a request when the operation type matches and the
    /// grant's TargetPrefix is a prefix of (or equal to) the requested target.
    /// </summary>
    public bool IsCovered(PermissionRequest request)
    {
        EnsureWorkspaceGrantsLoaded();
        EnsureAlwaysGrantsLoaded();

        return IsListCovered(_sessionGrants, request)
            || IsListCovered(_workspaceGrants!, request)
            || IsListCovered(_alwaysGrants!, request);
    }

    /// <summary>
    /// Stores a grant. Once-scoped grants are never persisted — they are valid for
    /// a single invocation and are not tracked after that. Session grants are held
    /// in memory. Workspace and Always grants are appended to their JSONL files.
    /// </summary>
    public async Task StoreAsync(PermissionGrant grant, CancellationToken ct = default)
    {
        switch (grant.Scope)
        {
            case PermissionScope.Once:
                // Once grants are not tracked; the caller handles the single-use logic.
                return;

            case PermissionScope.Session:
                _sessionGrants.Add(grant);
                break;

            case PermissionScope.Workspace:
                EnsureWorkspaceGrantsLoaded();
                _workspaceGrants!.Add(grant);
                await AppendGrantAsync(_workspacePermissionsPath, grant, ct).ConfigureAwait(false);
                break;

            case PermissionScope.Always:
                EnsureAlwaysGrantsLoaded();
                _alwaysGrants!.Add(grant);
                await AppendGrantAsync(_alwaysPermissionsPath, grant, ct).ConfigureAwait(false);
                break;

            default:
                throw new ArgumentOutOfRangeException(nameof(grant));
        }
    }

    private static bool IsListCovered(IEnumerable<PermissionGrant> grants, PermissionRequest request) =>
        grants.Any(grant => grant.OperationType == request.OperationType
            && (grant.TargetPrefix.Length == 0
                || request.Target.StartsWith(grant.TargetPrefix, StringComparison.Ordinal)));

    private void EnsureWorkspaceGrantsLoaded()
    {
        if (_workspaceGrants is not null)
            return;

        _workspaceGrants = LoadGrants(_workspacePermissionsPath);
    }

    private void EnsureAlwaysGrantsLoaded()
    {
        if (_alwaysGrants is not null)
            return;

        _alwaysGrants = LoadGrants(_alwaysPermissionsPath);
    }

    private static List<PermissionGrant> LoadGrants(string path)
    {
        var grants = new List<PermissionGrant>();

        try
        {
            foreach (var line in File.ReadLines(path))
            {
                if (string.IsNullOrWhiteSpace(line))
                    continue;

                try
                {
                    var grant = JsonSerializer.Deserialize(line, JsonContext.PermissionGrant);
                    if (grant is not null)
                        grants.Add(grant);
                }
                catch (JsonException)
                {
                    // Skip malformed lines — don't crash the host because of a bad grant file.
                }
            }
        }
        catch (IOException)
        {
            // File doesn't exist yet or can't be read — return empty grants.
        }

        return grants;
    }

    private static async Task AppendGrantAsync(string path, PermissionGrant grant, CancellationToken ct)
    {
        // Ensure the directory exists before writing.
        var directory = Path.GetDirectoryName(path);
        if (directory is not null)
            Directory.CreateDirectory(directory);

        var line = JsonSerializer.Serialize(grant, JsonContext.PermissionGrant) + "\n";
        await File.AppendAllTextAsync(path, line, ct).ConfigureAwait(false);
    }
}

/// <summary>
/// Source-generated JSON serialization context for AoT compatibility.
/// The actual options (naming policy, enum converters) are supplied at construction
/// time in <see cref="PermissionGrantStore"/> so that enum values serialize as
/// camelCase strings.
/// </summary>
[JsonSerializable(typeof(PermissionGrant))]
internal partial class PermissionGrantJsonContext : JsonSerializerContext;
