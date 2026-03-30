namespace Ur.Tui.Dummy;

public sealed record DummyModelInfo(
    string Id,
    string Name,
    int ContextLength,
    decimal InputCostPerToken,
    decimal OutputCostPerToken);
