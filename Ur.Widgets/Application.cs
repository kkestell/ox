using System.Collections.Concurrent;
using Ur.Console;

namespace Ur.Widgets;

/// <summary>
/// Base class for OOP-style TUI applications. Subclasses build their widget tree
/// in the constructor, store references to any widgets they need to mutate, and
/// override hooks like OnInput to react to user interaction.
///
/// This is the WinForms-style counterpart to the MVU IApp/AppRunner pattern:
/// instead of an immutable model and pure View function, the application owns
/// a persistent mutable widget tree and mutates it directly.
///
/// Threading model: all widget mutation happens on the single UI thread (the
/// thread that calls Run). External code on background threads can schedule
/// work on the UI thread via Invoke(Action), following the same pattern as
/// WinForms' Control.Invoke(). This means widget code never needs locking.
/// </summary>
public abstract class Application
{
    /// <summary>
    /// The root of the persistent widget tree. Subclasses set this in their
    /// constructor after building the UI.
    /// </summary>
    protected Widget Root { get; set; } = null!;

    /// <summary>
    /// Shared work queue that the main loop drains. Keyboard input from the
    /// background reader thread and external code via Invoke() both post here.
    /// Using a BlockingCollection so the main loop can sleep until work arrives
    /// without polling — Take() blocks, TryTake() drains remaining items.
    /// </summary>
    private readonly BlockingCollection<Action> _queue = new();

    /// <summary>
    /// Schedules an action to run on the UI thread during the next iteration
    /// of the main loop. Thread-safe — can be called from any thread.
    ///
    /// This is the primary API for external event sources (network listeners,
    /// background tasks, timers) to update the UI. The action runs on the UI
    /// thread, so it can safely mutate widgets without locking.
    /// </summary>
    /// <param name="action">
    /// The work to execute on the UI thread. Typically a closure that mutates
    /// one or more widgets, e.g. <c>app.Invoke(() => label.Text = msg)</c>.
    /// </param>
    public void Invoke(Action action) => _queue.Add(action);

    /// <summary>
    /// Starts the event loop with the given terminal driver. Owns the full lifecycle:
    /// driver init, focus management, queue-driven event dispatch, layout → render →
    /// present, and driver cleanup on exit.
    ///
    /// The loop is event-driven: a background thread reads console input and posts
    /// it to a shared queue. The main thread blocks on the queue, drains all pending
    /// actions, then re-renders. External code posts to the same queue via Invoke().
    /// </summary>
    public void Run(IDriver driver)
    {
        using (driver)
        {
            driver.Init();

            // Build the focus ring once at startup by collecting all focusable widgets
            // in depth-first order. The ring is stable because the tree is persistent —
            // no widgets are added or removed during the loop.
            var focusRing = CollectFocusable(Root);
            var focusIndex = 0;

            if (focusRing.Count > 0)
                focusRing[0].IsFocused = true;

            var running = true;

            // Keyboard input becomes just another producer posting to the queue.
            // A dedicated background thread calls the (blocking) driver.ReadInput()
            // and wraps each input event in an action that contains the dispatch
            // logic. This keeps the main loop simple: drain queue → render.
            var inputThread = new Thread(() =>
            {
                try
                {
                    while (true)
                    {
                        var input = driver.ReadInput();

                        // Package input dispatch as a closure that will run on the
                        // UI thread. All widget mutation stays single-threaded.
                        _queue.Add(() =>
                        {
                            // Ctrl-C is a universal hard-quit — sets the flag that
                            // the main loop checks after draining.
                            if (input is KeyEvent { Key: Key.CtrlC })
                            {
                                running = false;
                                return;
                            }

                            if (input is KeyEvent { Key: Key.Tab } && focusRing.Count > 0)
                            {
                                // Move focus forward through the ring.
                                focusRing[focusIndex].IsFocused = false;
                                focusIndex = (focusIndex + 1) % focusRing.Count;
                                focusRing[focusIndex].IsFocused = true;
                            }
                            else if (focusRing.Count > 0)
                            {
                                // Dispatch all other input to the focused widget.
                                focusRing[focusIndex].HandleInput(input);
                            }
                        });
                    }
                }
                catch (ObjectDisposedException)
                {
                    // driver.ReadInput() throws when the driver is disposed during
                    // shutdown while ReadInput() is blocking. Exit silently.
                    // Must be caught before InvalidOperationException since
                    // ObjectDisposedException inherits from it.
                }
                catch (InvalidOperationException)
                {
                    // _queue.Add() throws when CompleteAdding() has been called —
                    // the app is shutting down. Exit silently.
                }
            })
            {
                IsBackground = true,
                Name = "Ur.InputReader",
            };
            inputThread.Start();

            // Render the initial frame before any input arrives, so the user
            // sees the UI immediately rather than a blank screen.
            Root.X = 0;
            Root.Y = 0;
            Root.Layout(driver.Width, driver.Height);
            var screen = Renderer.Render(Root);
            driver.Present(screen);

            // When OX_DUMP_TREE is active the caller wants one-shot layout diagnostics:
            // exit immediately after the first frame so stderr output is readable.
            if (!string.IsNullOrEmpty(Environment.GetEnvironmentVariable("OX_DUMP_TREE")))
                return;

            // Main loop: block until work arrives, drain everything, re-render.
            // This is the heart of the event-driven design — the loop doesn't know
            // or care whether work came from keyboard input or Invoke().
            while (running)
            {
                // Block until at least one action is available.
                var action = _queue.Take();
                action();

                // Drain any remaining queued actions before rendering, so a burst
                // of events (e.g. rapid typing or multiple Invoke calls) coalesces
                // into a single render pass.
                while (_queue.TryTake(out var next))
                    next();

                // Check after draining — Ctrl-C might have been in the batch.
                if (!running) break;

                Root.X = 0;
                Root.Y = 0;
                Root.Layout(driver.Width, driver.Height);
                screen = Renderer.Render(Root);
                driver.Present(screen);
            }

            // Signal the background thread to stop. Its next Add() will throw
            // InvalidOperationException, which it catches and exits cleanly.
            _queue.CompleteAdding();
        }
    }

    /// <summary>
    /// Depth-first traversal to collect all widgets with Focusable == true.
    /// The order determines Tab cycling order — depth-first matches visual
    /// top-to-bottom for a vertical Stack layout, which feels natural.
    /// </summary>
    private static List<Widget> CollectFocusable(Widget root)
    {
        var result = new List<Widget>();
        var stack = new Stack<Widget>();
        stack.Push(root);

        while (stack.Count > 0)
        {
            var widget = stack.Pop();
            if (widget.Focusable)
                result.Add(widget);

            // Push children in reverse order so the first child is popped first,
            // preserving visual ordering in the focus ring.
            for (var i = widget.Children.Count - 1; i >= 0; i--)
                stack.Push(widget.Children[i]);
        }

        return result;
    }
}
