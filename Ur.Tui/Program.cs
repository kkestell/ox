using Ur.Terminal.App;
using Ur.Terminal.Input;
using Ur.Terminal.Rendering;
using Ur.Terminal.Terminal;
using Ur.Tui;
using Ur.Tui.Dummy;

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

var config = new DummyConfiguration();
var app = new ChatApp(config, compositor, baseLayer, overlayLayer);

var renderLoop = new RenderLoop(terminal, compositor, keyReader, targetFps: 30);

using var cts = new CancellationTokenSource();
Console.CancelKeyPress += (_, e) =>
{
    e.Cancel = true;
    cts.Cancel();
};

try
{
    await renderLoop.RunAsync(app.ProcessFrame, cts.Token);
}
finally
{
    keyReader.Stop();
}
