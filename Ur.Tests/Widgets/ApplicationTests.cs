using System.Collections.Concurrent;
using Ur.Console;
using Ur.Drawing;
using Ur.Widgets;
using Xunit;

namespace Ur.Tests.Widgets;

/// <summary>
/// Fake IDriver for testing the Application event loop. Input events are fed
/// via a BlockingCollection, and ReadInput() blocks until one is available —
/// exactly like the real ConsoleDriver, but under test control.
/// </summary>
class FakeDriver : IDriver
{
    private readonly BlockingCollection<InputEvent> _inputs = new();

    public int Width => 80;
    public int Height => 24;
    public int PresentCount { get; private set; }

    public void Init() { }
    public void Dispose() { }

    public void Present(Screen screen) => PresentCount++;

    /// <summary>
    /// Blocks until the test feeds an input event, mimicking real terminal
    /// blocking behavior.
    /// </summary>
    public InputEvent ReadInput() => _inputs.Take();

    /// <summary>
    /// Feeds an input event from the test thread. The background input
    /// reader thread inside Application will unblock and process it.
    /// </summary>
    public void FeedInput(InputEvent input) => _inputs.Add(input);
}

/// <summary>
/// Minimal Application subclass for testing. Exposes the label so tests can
/// verify that Invoke()'d mutations actually take effect.
/// </summary>
class TestApp : Application
{
    public Label TestLabel { get; }

    public TestApp()
    {
        TestLabel = new Label("initial");
        Root = TestLabel;
    }
}

public class ApplicationTests
{
    /// <summary>
    /// Verifies that an action posted via Invoke() executes on the UI thread
    /// and its mutation is visible after the loop exits. This is the core
    /// contract: external code can safely update widgets through Invoke().
    /// </summary>
    [Fact]
    public void Invoke_ExecutesActionOnUIThread()
    {
        var driver = new FakeDriver();
        var app = new TestApp();
        int? invokeThreadId = null;
        int? mainThreadId = null;

        // Schedule an Invoke before Run() starts — it should execute on the
        // first iteration since BlockingCollection accepts items before Take().
        app.Invoke(() =>
        {
            invokeThreadId = Environment.CurrentManagedThreadId;
            app.TestLabel.Text = "updated";
        });

        // Follow the Invoke with Ctrl-C so the loop drains the invoke action,
        // renders, then processes Ctrl-C and exits.
        Task.Run(() =>
        {
            // Small delay to ensure Run() has started and the initial render
            // has completed before we send Ctrl-C.
            Thread.Sleep(100);
            driver.FeedInput(new KeyEvent(Key.CtrlC));
        });

        // Run() blocks the calling thread — that's our "UI thread".
        mainThreadId = Environment.CurrentManagedThreadId;
        app.Run(driver);

        Assert.Equal("updated", app.TestLabel.Text);
        // The Invoke'd action must have run on the same thread that called Run().
        Assert.Equal(mainThreadId, invokeThreadId);
    }

    /// <summary>
    /// Verifies that keyboard input from the driver is dispatched through
    /// the queue and reaches the focused widget. Characters typed on the
    /// background input thread must arrive on the UI thread and mutate widgets.
    /// </summary>
    [Fact]
    public void KeyboardInput_DispatchesThroughQueue()
    {
        var driver = new FakeDriver();
        var app = new TextInputApp();

        Task.Run(async () =>
        {
            // Give Run() time to start the background input thread and render
            // the initial frame.
            await Task.Delay(100);

            // Type "Hi" into the focused TextInput.
            driver.FeedInput(new KeyEvent(Key.Character, 'H'));
            driver.FeedInput(new KeyEvent(Key.Character, 'i'));

            // Small delay to let the actions drain and render.
            await Task.Delay(100);

            driver.FeedInput(new KeyEvent(Key.CtrlC));
        });

        app.Run(driver);

        Assert.Equal("Hi", app.Input.Value);
    }

    /// <summary>
    /// Verifies that Ctrl-C causes a clean shutdown: the loop exits, the driver
    /// is disposed, and no exceptions leak out.
    /// </summary>
    [Fact]
    public void CtrlC_CausesCleanShutdown()
    {
        var driver = new FakeDriver();
        var app = new TestApp();

        Task.Run(async () =>
        {
            await Task.Delay(100);
            driver.FeedInput(new KeyEvent(Key.CtrlC));
        });

        // If shutdown is clean, Run() returns normally. If not, this will
        // hang or throw — the test framework's timeout catches hangs.
        app.Run(driver);

        // The initial frame should have been presented at minimum.
        Assert.True(driver.PresentCount >= 1);
    }

    /// <summary>
    /// Verifies that multiple Invoke() calls coalesce into a single render pass.
    /// The queue drains all pending actions before re-rendering, so burst updates
    /// don't cause redundant layout/render cycles.
    /// </summary>
    [Fact]
    public void MultipleInvokes_CoalesceIntoSingleRender()
    {
        var driver = new FakeDriver();
        var app = new TestApp();

        Task.Run(async () =>
        {
            await Task.Delay(100);

            // Post multiple mutations in quick succession — they should all
            // drain in one pass before a single re-render.
            app.Invoke(() => app.TestLabel.Text = "one");
            app.Invoke(() => app.TestLabel.Text = "two");
            app.Invoke(() => app.TestLabel.Text = "three");

            await Task.Delay(100);
            driver.FeedInput(new KeyEvent(Key.CtrlC));
        });

        app.Run(driver);

        // The final value should be the last mutation in the batch.
        Assert.Equal("three", app.TestLabel.Text);

        // 1 initial render + 1 render after the coalesced batch = 2.
        // (Ctrl-C doesn't trigger a render since the loop breaks.)
        Assert.Equal(2, driver.PresentCount);
    }
}

/// <summary>
/// Application subclass with a focusable TextInput for testing keyboard dispatch.
/// </summary>
class TextInputApp : Application
{
    public TextInput Input { get; }

    public TextInputApp()
    {
        Input = new TextInput();
        Root = Input;
    }
}
