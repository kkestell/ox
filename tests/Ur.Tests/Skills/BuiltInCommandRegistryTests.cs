using Ur.Skills;

namespace Ur.Tests.Skills;

public sealed class BuiltInCommandRegistryTests
{
    private readonly BuiltInCommandRegistry _registry = new();

    [Fact]
    public void All_ContainsExpectedCoreCommands()
    {
        var names = _registry.All.Select(c => c.Name).ToList();

        Assert.Contains("clear", names);
        Assert.Contains("model", names);
        Assert.Contains("quit", names);
        Assert.Contains("set", names);
    }

    [Fact]
    public void Get_ExistingCommand_ReturnsCommand()
    {
        var cmd = _registry.Get("quit");

        Assert.NotNull(cmd);
        Assert.Equal("quit", cmd.Name);
    }

    [Fact]
    public void Get_CaseInsensitive()
    {
        Assert.NotNull(_registry.Get("CLEAR"));
        Assert.NotNull(_registry.Get("Clear"));
        Assert.NotNull(_registry.Get("clear"));
    }

    [Fact]
    public void Get_UnknownCommand_ReturnsNull()
    {
        Assert.Null(_registry.Get("unknown-command-xyz"));
    }
}
