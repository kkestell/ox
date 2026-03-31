using Ur.Terminal.Layout;

namespace Ur.Terminal.Tests;

public class SizeConstraintTests
{
    [Fact]
    public void Fixed_StoresSize()
    {
        var constraint = new SizeConstraint.Fixed(10);

        Assert.Equal(10, constraint.Size);
    }

    [Fact]
    public void Fixed_ZeroSize_IsValid()
    {
        var constraint = new SizeConstraint.Fixed(0);

        Assert.Equal(0, constraint.Size);
    }

    [Fact]
    public void Fixed_NegativeSize_Throws()
    {
        Assert.Throws<ArgumentOutOfRangeException>(() => new SizeConstraint.Fixed(-1));
    }

    [Fact]
    public void Fill_DefaultWeight_IsOne()
    {
        var constraint = new SizeConstraint.Fill();

        Assert.Equal(1, constraint.Weight);
    }

    [Fact]
    public void Fill_CustomWeight_IsStored()
    {
        var constraint = new SizeConstraint.Fill(3);

        Assert.Equal(3, constraint.Weight);
    }

    [Fact]
    public void Fill_ZeroWeight_Throws()
    {
        Assert.Throws<ArgumentOutOfRangeException>(() => new SizeConstraint.Fill(0));
    }

    [Fact]
    public void Content_IsDistinctType()
    {
        SizeConstraint constraint = new SizeConstraint.Content();

        Assert.IsType<SizeConstraint.Content>(constraint);
    }

    [Fact]
    public void PatternMatching_WorksForAllSubtypes()
    {
        SizeConstraint[] constraints =
        [
            new SizeConstraint.Fixed(5),
            new SizeConstraint.Content(),
            new SizeConstraint.Fill(2),
        ];

        var results = constraints.Select(c => c switch
        {
            SizeConstraint.Fixed f => $"fixed:{f.Size}",
            SizeConstraint.Content => "content",
            SizeConstraint.Fill f => $"fill:{f.Weight}",
            _ => "unknown",
        }).ToArray();

        Assert.Equal(["fixed:5", "content", "fill:2"], results);
    }
}
