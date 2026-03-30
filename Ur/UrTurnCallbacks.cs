using Ur.Permissions;

namespace Ur;

public sealed class UrTurnCallbacks
{
    public Func<PermissionRequest, CancellationToken, ValueTask<PermissionResponse>>?
        RequestPermissionAsync { get; init; }
}
