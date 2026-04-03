using Ur.Permissions;

namespace Ur;

public sealed class TurnCallbacks
{
    public Func<PermissionRequest, CancellationToken, ValueTask<PermissionResponse>>?
        RequestPermissionAsync { get; init; }
}
