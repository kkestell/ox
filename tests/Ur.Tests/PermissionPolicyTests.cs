using Ur.Permissions;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="PermissionPolicy"/>. This class defines the security
/// boundaries of the permission system: which operation types need user approval
/// and which scopes are available. These tests pin the policy contract so that
/// changes to the security model are intentional and visible.
/// </summary>
public sealed class PermissionPolicyTests
{
    // ─── RequiresPrompt ───────────────────────────────────────────────

    [Fact]
    public void RequiresPrompt_ReadInWorkspace_NeverPrompts()
    {
        // Reading workspace files is the lowest-risk operation — the model
        // must be able to do this freely without interrupting the user.
        Assert.False(PermissionPolicy.RequiresPrompt(OperationType.ReadInWorkspace));
    }

    [Fact]
    public void RequiresPrompt_WriteInWorkspace_RequiresPrompt()
    {
        Assert.True(PermissionPolicy.RequiresPrompt(OperationType.WriteInWorkspace));
    }

    [Fact]
    public void RequiresPrompt_ReadOutsideWorkspace_RequiresPrompt()
    {
        Assert.True(PermissionPolicy.RequiresPrompt(OperationType.ReadOutsideWorkspace));
    }

    [Fact]
    public void RequiresPrompt_ExecuteCommand_RequiresPrompt()
    {
        Assert.True(PermissionPolicy.RequiresPrompt(OperationType.ExecuteCommand));
    }

    // ─── AllowedScopes ────────────────────────────────────────────────

    [Fact]
    public void AllowedScopes_ReadInWorkspace_ReturnsEmpty()
    {
        // No grant needed — auto-allowed. Empty scopes means the user
        // should never see a scope picker for this operation type.
        var scopes = PermissionPolicy.AllowedScopes(OperationType.ReadInWorkspace);
        Assert.Empty(scopes);
    }

    [Fact]
    public void AllowedScopes_WriteInWorkspace_AllScopesAvailable()
    {
        // Writing is the most nuanced operation — the user should be able
        // to grant it with any duration from one-shot to permanent.
        var scopes = PermissionPolicy.AllowedScopes(OperationType.WriteInWorkspace);

        Assert.Contains(PermissionScope.Once, scopes);
        Assert.Contains(PermissionScope.Session, scopes);
        Assert.Contains(PermissionScope.Workspace, scopes);
        Assert.Contains(PermissionScope.Always, scopes);
    }

    [Fact]
    public void AllowedScopes_ExecuteCommand_OnlyOnce()
    {
        // Executing commands is inherently risky — only one-shot grants
        // prevent the model from silently running commands in perpetuity.
        var scopes = PermissionPolicy.AllowedScopes(OperationType.ExecuteCommand);

        Assert.Single(scopes);
        Assert.Equal(PermissionScope.Once, scopes[0]);
    }

    [Fact]
    public void AllowedScopes_ReadOutsideWorkspace_OnlyOnce()
    {
        var scopes = PermissionPolicy.AllowedScopes(OperationType.ReadOutsideWorkspace);

        Assert.Single(scopes);
        Assert.Equal(PermissionScope.Once, scopes[0]);
    }
}
