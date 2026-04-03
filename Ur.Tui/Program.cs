using dotenv.net;
using Ur;
using Ur.Terminal.App;
using Ur.Terminal.Input;
using Ur.Terminal.Rendering;
using Ur.Terminal.Terminal;
using Ur.Tui;

// Boot the Ur host for the current working directory. This loads settings,
// discovers extensions, and prepares the model catalog.
var host = await UrHost.StartAsync(Environment.CurrentDirectory);

// Ensure model catalog is populated (fetches from OpenRouter if cache is empty).
// This is done eagerly so the user doesn't hit a delay when opening the model
// picker for the first time.
if (host.Configuration.AvailableModels.Count == 0)
    await host.Configuration.RefreshModelsAsync();

// Wrap the host in the TUI's backend interface. ChatBackend is a thin adapter
// that satisfies IChatBackend, allowing ChatApp to be tested with a mock.
var backend = new ChatBackend(host);

// Set up the terminal: raw mode disables line buffering and echo so we can
// process individual key presses; alternate buffer keeps the shell history
// intact when the app exits; hidden cursor avoids flickering during redraws.
using var terminal = new AnsiTerminal();
terminal.EnterRawMode();
terminal.EnterAlternateBuffer();
terminal.HideCursor();

// The compositor manages layers — base (chat content) and overlay (modals).
// Layers are composited back-to-front each frame and only dirty regions are
// written to the terminal to minimize output.
var compositor = new Compositor(terminal.Width, terminal.Height);

var baseLayer = new Layer(0, 0, terminal.Width, terminal.Height);
compositor.AddLayer(baseLayer);

var overlayLayer = new Layer(0, 0, terminal.Width, terminal.Height);
compositor.AddLayer(overlayLayer);

// KeyReader runs a background thread that reads raw escape sequences from
// stdin and decodes them into KeyEvent structs. The events are queued and
// drained by the render loop each frame.
var keyReader = new KeyReader(terminal);
keyReader.Start(CancellationToken.None);

var app = new ChatApp(backend, compositor, baseLayer, overlayLayer);

// The render loop drives the application at 30 FPS. Each frame:
//   1. Drain key events from the reader
//   2. Call app.ProcessFrame (handles input, processes agent events, renders)
//   3. Diff the compositor layers against the previous frame and write changes
var renderLoop = new RenderLoop(terminal, compositor, keyReader, targetFps: 30);

// Ctrl+C cancels the render loop cleanly so the finally block can restore
// the terminal state.
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
    // Stopping the key reader restores terminal settings (cleanup is handled
    // by AnsiTerminal.Dispose via the using declaration above).
    keyReader.Stop();
}
