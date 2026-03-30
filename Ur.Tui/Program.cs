using Ur.Terminal.App;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Terminal.Rendering;
using Ur.Terminal.Terminal;
using Buffer = Ur.Terminal.Core.Buffer;

using var terminal = new AnsiTerminal();
terminal.EnterRawMode();
terminal.EnterAlternateBuffer();
terminal.HideCursor();

var compositor = new Compositor(terminal.Width, terminal.Height);

var baseLayer = new Layer(0, 0, terminal.Width, terminal.Height);
compositor.AddLayer(baseLayer);

var overlayLayer = new Layer(0, 0, terminal.Width, terminal.Height);
compositor.AddLayer(overlayLayer);

var keyReader = new KeyReader(terminal);
keyReader.Start(CancellationToken.None);

var renderLoop = new RenderLoop(terminal, compositor, keyReader, targetFps: 30);

var frame = 0;
var showModal = false;
var lastKey = "(none)";

using var cts = new CancellationTokenSource();
Console.CancelKeyPress += (_, e) =>
{
    e.Cancel = true;
    cts.Cancel();
};

try
{
    await renderLoop.RunAsync(keys =>
    {
        frame++;

        // Handle input
        foreach (var key in keys)
        {
            if (key.Key == Key.Q && key.Mods == Modifiers.None)
                return false;
            if (key.Key == Key.C && key.Mods == Modifiers.Ctrl)
                return false;
            if (key.Key == Key.M && key.Mods == Modifiers.None)
                showModal = !showModal;

            lastKey = key.Char.HasValue
                ? $"{key.Key} ('{key.Char}')"
                : $"{key.Key}";
            if (key.Mods != Modifiers.None)
                lastKey = $"{key.Mods}+{lastKey}";
        }

        // Render base layer
        var w = compositor.Width;
        var h = compositor.Height;
        baseLayer.Clear();
        if (baseLayer.Width != w || baseLayer.Height != h)
            baseLayer.Resize(w, h);

        var fg = new Color(0, 200, 200);
        var bg = Color.Black;
        var white = Color.White;

        baseLayer.Content.WriteString(2, 1, "── Ur.Tui Framework Demo ──", fg, bg);
        baseLayer.Content.WriteString(4, 3, $"Frame: {frame}", white, bg);
        baseLayer.Content.WriteString(4, 4, $"Size:  {w}x{h}", white, bg);
        baseLayer.Content.WriteString(4, 5, $"Key:   {lastKey}", white, bg);
        baseLayer.Content.WriteString(4, 7, "Press 'm' to toggle modal, 'q' to quit", new Color(128, 128, 128), bg);

        // Render overlay
        overlayLayer.Clear();
        if (overlayLayer.Width != w || overlayLayer.Height != h)
            overlayLayer.Resize(w, h);

        if (showModal)
        {
            var modalW = 40;
            var modalH = 10;
            var mx = (w - modalW) / 2;
            var my = (h - modalH) / 2;
            var modalRect = new Rect(mx, my, modalW, modalH);

            // Shadow: L-shaped strip offset from modal (right edge + bottom edge)
            // Right strip: 2 columns wide, full modal height, to the right of modal
            overlayLayer.MarkShadow(new Rect(mx + modalW, my + 1, 2, modalH));
            // Bottom strip: full modal width, 1 row tall, below the modal
            overlayLayer.MarkShadow(new Rect(mx + 2, my + modalH, modalW, 1));

            // Modal box
            var modalFg = new Color(220, 220, 220);
            var modalBg = new Color(30, 30, 60);
            overlayLayer.Content.Fill(modalRect, new Cell(' ', modalFg, modalBg));
            overlayLayer.Content.DrawBox(modalRect, modalFg, modalBg);
            overlayLayer.Content.WriteString(mx + 2, my + 2, "  Modal Overlay", new Color(255, 255, 100), modalBg);
            overlayLayer.Content.WriteString(mx + 2, my + 4, "  This is composited on top", modalFg, modalBg);
            overlayLayer.Content.WriteString(mx + 2, my + 5, "  with a shadow effect.", modalFg, modalBg);
            overlayLayer.Content.WriteString(mx + 2, my + 7, "  Press 'm' to close", new Color(128, 128, 128), modalBg);
        }

        return true;
    }, cts.Token);
}
finally
{
    keyReader.Stop();
}
