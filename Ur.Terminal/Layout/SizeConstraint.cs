namespace Ur.Terminal.Layout;

/// <summary>How a child in a layout container expresses its desired height.</summary>
public abstract record SizeConstraint
{
    private SizeConstraint() { }

    /// <summary>Exactly <paramref name="Size"/> rows.</summary>
    public sealed record Fixed(int Size) : SizeConstraint
    {
        public Fixed() : this(0) { }
        public int Size { get; init; } = Size >= 0 ? Size : throw new ArgumentOutOfRangeException(nameof(Size));
    }

    /// <summary>Use the widget's <c>MeasureHeight</c>.</summary>
    public sealed record Content() : SizeConstraint;

    /// <summary>Take remaining space, proportional to weight.</summary>
    public sealed record Fill(int Weight = 1) : SizeConstraint
    {
        public int Weight { get; init; } = Weight >= 1 ? Weight : throw new ArgumentOutOfRangeException(nameof(Weight));
    }
}
