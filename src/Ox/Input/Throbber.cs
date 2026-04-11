using Te.Rendering;

namespace Ox.Input;

/// <summary>
/// Animated activity indicator that displays an 8-bit binary counter as
/// circle glyphs. Each bit maps to one circle: set bits render in the
/// active color, unset bits in the inactive color. The counter increments
/// every tick (called once per second from the main loop).
///
/// The throbber starts at 1 when a turn begins and advances with each tick.
/// It resets to 0 when the turn ends (hiding the display).
/// </summary>
public sealed class Throbber
{
    private const int BitCount = 8;
    private const char Circle = '●';

    private int _counter;

    /// <summary>Current counter value. 0 means inactive (hidden).</summary>
    public int Counter => _counter;

    /// <summary>Start the throbber by setting the counter to 1.</summary>
    public void Start()
    {
        _counter = 1;
    }

    /// <summary>Increment the counter by 1.</summary>
    public void Tick()
    {
        _counter++;
    }

    /// <summary>Reset the counter to 0 (inactive).</summary>
    public void Reset()
    {
        _counter = 0;
    }

    /// <summary>
    /// Render the 8-bit counter as circle glyphs into the buffer at position (x, y).
    /// Bits are rendered MSB-first (bit 7 on the left). Circles are separated
    /// by spaces, consuming 15 columns total (8 circles + 7 spaces).
    /// </summary>
    public void Render(ConsoleBuffer buffer, int x, int y, Color activeColor, Color inactiveColor)
    {
        for (var bit = BitCount - 1; bit >= 0; bit--)
        {
            var isSet = (_counter & (1 << bit)) != 0;
            var color = isSet ? activeColor : inactiveColor;
            buffer.SetCell(x, y, Circle, color, Color.Default);
            x++;

            // Space separator between circles (except after the last one).
            if (bit > 0)
            {
                buffer.SetCell(x, y, ' ', Color.Default, Color.Default);
                x++;
            }
        }
    }

    /// <summary>Total columns consumed by the throbber display.</summary>
    public static int RenderWidth => (BitCount * 2) - 1; // 8 circles + 7 spaces = 15
}
