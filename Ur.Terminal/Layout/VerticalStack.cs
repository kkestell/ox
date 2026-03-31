using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Terminal.Layout;

/// <summary>Widget that partitions a rect vertically among children.</summary>
public sealed class VerticalStack : Widget
{
    public readonly record struct Entry(Widget Child, SizeConstraint Height);

    private readonly IReadOnlyList<Entry> _entries;

    public VerticalStack(IReadOnlyList<Entry> entries)
    {
        _entries = entries;
    }

    public VerticalStack(params Entry[] entries)
    {
        _entries = entries;
    }

    protected override void RenderContent(Buffer buffer, Rect area)
    {
        var heights = ComputeHeights(area.Width, area.Height);

        var y = area.Y;
        for (var i = 0; i < _entries.Count; i++)
        {
            var h = heights[i];
            if (h <= 0)
                continue;
            var childRect = new Rect(area.X, y, area.Width, h);
            _entries[i].Child.Render(buffer, childRect);
            y += h;
        }
    }

    public override bool HandleKey(KeyEvent key) => false;

    private int[] ComputeHeights(int width, int availableHeight)
    {
        var heights = new int[_entries.Count];
        var claimed = 0;
        var totalFillWeight = 0;

        // Pass 1: measure Fixed and Content children.
        for (var i = 0; i < _entries.Count; i++)
        {
            switch (_entries[i].Height)
            {
                case SizeConstraint.Fixed f:
                    heights[i] = f.Size;
                    claimed += f.Size;
                    break;

                case SizeConstraint.Content:
                    var measured = _entries[i].Child.MeasureHeight(width) ?? 0;
                    heights[i] = measured;
                    claimed += measured;
                    break;

                case SizeConstraint.Fill f:
                    totalFillWeight += f.Weight;
                    break;
            }
        }

        // Pass 2: distribute remaining space among Fill children.
        var remaining = Math.Max(0, availableHeight - claimed);
        if (totalFillWeight > 0 && remaining > 0)
        {
            var distributed = 0;
            for (var i = 0; i < _entries.Count; i++)
            {
                if (_entries[i].Height is SizeConstraint.Fill f)
                {
                    var share = remaining * f.Weight / totalFillWeight;
                    heights[i] = share;
                    distributed += share;
                }
            }

            // Give rounding remainder to the last Fill child.
            var leftover = remaining - distributed;
            if (leftover > 0)
            {
                for (var i = _entries.Count - 1; i >= 0; i--)
                {
                    if (_entries[i].Height is SizeConstraint.Fill)
                    {
                        heights[i] += leftover;
                        break;
                    }
                }
            }
        }

        // Clamp if total exceeds available.
        var total = heights.Sum();
        if (total > availableHeight)
        {
            var excess = total - availableHeight;
            // Shrink from the end.
            for (var i = _entries.Count - 1; i >= 0 && excess > 0; i--)
            {
                var shrink = Math.Min(heights[i], excess);
                heights[i] -= shrink;
                excess -= shrink;
            }
        }

        return heights;
    }
}
