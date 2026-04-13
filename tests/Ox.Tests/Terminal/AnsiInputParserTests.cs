using System.Text;
using Ox.Terminal.Input;

namespace Ox.Tests.Terminal;

public sealed class AnsiInputParserTests
{
    [Fact]
    public void Parse_SgrMousePress_ProducesMouseEvent()
    {
        var parser = new AnsiInputParser();

        var events = parser.Parse(Encoding.ASCII.GetBytes("\u001b[<0;11;6M"));

        var mouseEvent = Assert.IsType<MouseInputEvent>(Assert.Single(events));
        Assert.Equal(new Point(10, 5), mouseEvent.Mouse.Position);
        Assert.True(mouseEvent.Mouse.HasFlag(MouseFlags.Button1Pressed));
    }

    [Fact]
    public void Parse_SgrMouseRelease_ProducesMouseRelease()
    {
        var parser = new AnsiInputParser();

        var events = parser.Parse(Encoding.ASCII.GetBytes("\u001b[<0;11;6m"));

        var mouseEvent = Assert.IsType<MouseInputEvent>(Assert.Single(events));
        Assert.Equal(new Point(10, 5), mouseEvent.Mouse.Position);
        Assert.True(mouseEvent.Mouse.HasFlag(MouseFlags.Button1Released));
    }

    [Fact]
    public void Parse_SgrMouseWheel_ProducesWheelEvent()
    {
        var parser = new AnsiInputParser();

        var events = parser.Parse(Encoding.ASCII.GetBytes("\u001b[<64;11;6M"));

        var mouseEvent = Assert.IsType<MouseInputEvent>(Assert.Single(events));
        Assert.True(mouseEvent.Mouse.HasFlag(MouseFlags.WheeledUp));
    }

    [Fact]
    public void Parse_CsiArrowKey_ProducesNormalizedKeyEvent()
    {
        var parser = new AnsiInputParser();

        var events = parser.Parse(Encoding.ASCII.GetBytes("\u001b[A"));

        var keyEvent = Assert.IsType<KeyInputEvent>(Assert.Single(events));
        Assert.Equal(KeyCode.CursorUp, keyEvent.Key.KeyCode);
    }

    [Fact]
    public void Flush_AfterBareEscape_ProducesEscapeKey()
    {
        var parser = new AnsiInputParser();
        parser.Parse(Encoding.ASCII.GetBytes("\u001b"));

        var events = parser.Flush();

        var keyEvent = Assert.IsType<KeyInputEvent>(Assert.Single(events));
        Assert.Equal(KeyCode.Esc, keyEvent.Key.KeyCode);
    }
}
