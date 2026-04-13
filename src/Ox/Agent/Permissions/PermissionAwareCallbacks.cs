namespace Ox.Agent.Permissions;

/// <summary>
/// Factory that layers grant-store checking in front of a host-provided
/// <see cref="TurnCallbacks"/>.
///
/// Lives here — alongside <see cref="PermissionGrantStore"/> — because the
/// decision logic is entirely about permissions. <see cref="Sessions.OxSession"/>
/// only holds the references and should not know *how* grants and host prompts
/// are combined.
///
/// Decision flow:
///   1. Grant store covers the request → approve immediately, no prompt.
///   2. No host callback configured → deny without prompting.
///   3. Host callback present → delegate; on a durable grant, persist to the store.
///
/// The returned callback is always non-null so AgentLoop only ever sees a simple
/// approve/deny surface. SubagentEventEmitted passes through unchanged — it has
/// nothing to do with permissions and the permission layer has no reason to
/// intercept it.
/// </summary>
internal static class PermissionAwareCallbacks
{
    public static TurnCallbacks Build(TurnCallbacks? hostCallbacks, PermissionGrantStore grantStore) =>
        new()
        {
            SubagentEventEmitted = hostCallbacks?.SubagentEventEmitted,

            RequestPermissionAsync = async (request, ct) =>
            {
                // Grant store already covers it — no need to bother the user.
                if (grantStore.IsCovered(request))
                    return new PermissionResponse(true, Scope: null);

                // No host callback: auto-deny. This keeps headless/test runs
                // deterministic when they didn't wire a prompt handler.
                if (hostCallbacks?.RequestPermissionAsync is null)
                    return new PermissionResponse(false, Scope: null);

                // Ask the host (CLI prompt, GUI dialog, etc).
                var response = await hostCallbacks.RequestPermissionAsync(request, ct)
                    .ConfigureAwait(false);

                // Persist durable grants so the user isn't re-asked next turn
                // (or next session). Once-scoped grants are single-use and not tracked.
                if (response is not { Granted: true, Scope: not null and not PermissionScope.Once })
                    return response;

                var grant = new PermissionGrant(
                    request.OperationType,
                    request.Target,
                    response.Scope.Value,
                    request.ToolName);

                await grantStore.StoreAsync(grant, ct).ConfigureAwait(false);

                return response;
            }
        };
}
