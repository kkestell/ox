using Ur.Permissions;

namespace Ur.Tests.Permissions;

/// <summary>
/// Tests for <see cref="PermissionPolicy"/>. This class defines the security
/// matrix: which (operation type, workspace containment) combinations require
/// user approval and which grant scopes are available. These tests pin the
/// policy contract so that changes to the security model are intentional and visible.
///
/// The matrix under test:
///   Read  + in-workspace  → no prompt
///   Read  + out-of-workspace → prompt (Once)
///   Write + in-workspace  → prompt (all scopes)
///   Write + out-of-workspace → prompt (Once)
///   Execute + any         → prompt (Once)
/// </summary>
public sealed class PermissionPolicyTests
{
    // ─── RequiresPrompt ───────────────────────────────────────────────

    [Fact]
    public void RequiresPrompt_Read_InWorkspace_NeverPrompts()
    {
        // Reading workspace files is the lowest-risk operation — the model
        // must be able to do this freely without interrupting the user.
        Assert.False(PermissionPolicy.RequiresPrompt(OperationType.Read, isInWorkspace: true));
    }

    [Fact]
    public void RequiresPrompt_Read_OutsideWorkspace_RequiresPrompt()
    {
        // Reading outside the workspace is sensitive — it could expose system files.
        Assert.True(PermissionPolicy.RequiresPrompt(OperationType.Read, isInWorkspace: false));
    }

    [Fact]
    public void RequiresPrompt_Write_InWorkspace_RequiresPrompt()
    {
        Assert.True(PermissionPolicy.RequiresPrompt(OperationType.Write, isInWorkspace: true));
    }

    [Fact]
    public void RequiresPrompt_Write_OutsideWorkspace_RequiresPrompt()
    {
        Assert.True(PermissionPolicy.RequiresPrompt(OperationType.Write, isInWorkspace: false));
    }

    [Fact]
    public void RequiresPrompt_Execute_InWorkspace_RequiresPrompt()
    {
        // Commands always prompt regardless of location — they can reach anything.
        Assert.True(PermissionPolicy.RequiresPrompt(OperationType.Execute, isInWorkspace: true));
    }

    [Fact]
    public void RequiresPrompt_Execute_OutsideWorkspace_RequiresPrompt()
    {
        Assert.True(PermissionPolicy.RequiresPrompt(OperationType.Execute, isInWorkspace: false));
    }

    // ─── AllowedScopes ────────────────────────────────────────────────

    [Fact]
    public void AllowedScopes_Read_InWorkspace_ReturnsEmpty()
    {
        // No grant needed — auto-allowed. Empty scopes means the user
        // should never see a scope picker for this combination.
        var scopes = PermissionPolicy.AllowedScopes(OperationType.Read, isInWorkspace: true);
        Assert.Empty(scopes);
    }

    [Fact]
    public void AllowedScopes_Read_OutsideWorkspace_OnlyOnce()
    {
        // Outside-workspace reads are sensitive — restrict to Once so the
        // user must explicitly re-approve rather than granting broad access.
        var scopes = PermissionPolicy.AllowedScopes(OperationType.Read, isInWorkspace: false);

        Assert.Single(scopes);
        Assert.Equal(PermissionScope.Once, scopes[0]);
    }

    [Fact]
    public void AllowedScopes_Write_InWorkspace_AllScopesAvailable()
    {
        // Writing inside the workspace is the most nuanced operation — the user
        // should be able to grant it with any duration from one-shot to permanent.
        var scopes = PermissionPolicy.AllowedScopes(OperationType.Write, isInWorkspace: true);

        Assert.Contains(PermissionScope.Once, scopes);
        Assert.Contains(PermissionScope.Session, scopes);
        Assert.Contains(PermissionScope.Workspace, scopes);
        Assert.Contains(PermissionScope.Always, scopes);
    }

    [Fact]
    public void AllowedScopes_Write_OutsideWorkspace_OnlyOnce()
    {
        // Writing outside the workspace is high-risk — Once-only prevents
        // the model from silently accumulating durable write grants outside the project.
        var scopes = PermissionPolicy.AllowedScopes(OperationType.Write, isInWorkspace: false);

        Assert.Single(scopes);
        Assert.Equal(PermissionScope.Once, scopes[0]);
    }

    [Fact]
    public void AllowedScopes_Execute_InWorkspace_OnlyOnce()
    {
        // Executing commands is inherently risky — only one-shot grants
        // prevent the model from silently running commands in perpetuity.
        var scopes = PermissionPolicy.AllowedScopes(OperationType.Execute, isInWorkspace: true);

        Assert.Single(scopes);
        Assert.Equal(PermissionScope.Once, scopes[0]);
    }

    [Fact]
    public void AllowedScopes_Execute_OutsideWorkspace_OnlyOnce()
    {
        var scopes = PermissionPolicy.AllowedScopes(OperationType.Execute, isInWorkspace: false);

        Assert.Single(scopes);
        Assert.Equal(PermissionScope.Once, scopes[0]);
    }
}
