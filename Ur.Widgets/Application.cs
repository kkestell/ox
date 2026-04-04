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
///
/// Modal dialogs: Application maintains a stack of modal dialogs that overlay
/// the main content. When a modal is active, input is scoped to the dialog's
/// widget subtree (the focus ring is rebuilt from the dialog, not Root), and
/// Escape dismisses the topmost dialog. This follows the WinForms ShowDialog
/// pattern — dialogs are peers to the main tree, not children of it.
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
    /// Stack of active modal dialogs. The topmost dialog receives all input
    /// and is rendered as a centered overlay. When empty, input goes to Root.
    /// This is a new concept in the widget system: widgets that exist outside
    /// the Root tree but participate in layout and rendering.
    /// </summary>
    private readonly Stack<Dialog> _modalStack = new();

    /// <summary>
    /// The current focus ring — all focusable widgets under the active focus
    /// root (topmost modal or Root). Stored as instance state so ShowModal and
    /// CloseModal can rebuild it without plumbing locals through the main loop.
    /// </summary>
    private List<Widget> _focusRing = new();
    private int _focusIndex;

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
    /// Pushes a dialog onto the modal stack, making it the active input target.
    /// The focus ring is rebuilt from the dialog's focusable widgets, so Tab
    /// only cycles between dialog controls. Subscribes to the dialog's Closed
    /// event so CloseModal() is called automatically when the dialog dismisses.
    /// </summary>
    public void ShowModal(Dialog dialog)
    {
        _modalStack.Push(dialog);

        // Auto-close: when the dialog fires Closed (OK button, Cancel button,
        // or Escape key), pop it off the stack and restore focus to whatever
        // is underneath (next modal or Root).
        dialog.Closed += _ => CloseModal();

        RebuildFocusRing();
    }

    /// <summary>
    /// Pops the topmost modal dialog and restores the focus ring to the next
    /// modal down (or Root if no modals remain). Called automatically by the
    /// Closed event subscription set up in ShowModal.
    /// </summary>
    public void CloseModal()
    {
        if (_modalStack.Count == 0) return;
        _modalStack.Pop();
        RebuildFocusRing();
    }

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

            // Build the initial focus ring from Root. ShowModal/CloseModal will
            // rebuild it from the active focus root when modals are pushed/popped.
            RebuildFocusRing();

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

                            // Escape dismisses the topmost modal with Cancel result.
                            // This fires the Closed event, which auto-pops via the
                            // subscription in ShowModal. Must check before Tab so
                            // Escape doesn't cycle focus.
                            if (input is KeyEvent { Key: Key.Escape } && _modalStack.Count > 0)
                            {
                                _modalStack.Peek().Close(DialogResult.Cancel);
                                return;
                            }

                            if (input is KeyEvent { Key: Key.Tab } && _focusRing.Count > 0)
                            {
                                // Move focus forward through the ring.
                                _focusRing[_focusIndex].IsFocused = false;
                                _focusIndex = (_focusIndex + 1) % _focusRing.Count;
                                _focusRing[_focusIndex].IsFocused = true;
                            }
                            else if (_focusRing.Count > 0)
                            {
                                // Dispatch all other input to the focused widget.
                                _focusRing[_focusIndex].HandleInput(input);
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
            RenderFrame(driver);

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

                RenderFrame(driver);
            }

            // Signal the background thread to stop. Its next Add() will throw
            // InvalidOperationException, which it catches and exits cleanly.
            _queue.CompleteAdding();
        }
    }

    /// <summary>
    /// Lays out and renders the full frame: Root tree first, then the topmost
    /// modal dialog as a centered overlay. Extracted from the main loop to avoid
    /// duplicating the layout+render+present sequence for the initial frame and
    /// the per-event render.
    /// </summary>
    private void RenderFrame(IDriver driver)
    {
        Root.X = 0;
        Root.Y = 0;
        Root.Layout(driver.Width, driver.Height);

        // Layout the topmost modal (if any) against the full terminal dimensions.
        // The Renderer will center it and dim the background.
        Dialog? topModal = _modalStack.Count > 0 ? _modalStack.Peek() : null;
        topModal?.Layout(driver.Width, driver.Height);

        var screen = Renderer.Render(Root, topModal);
        driver.Present(screen);
    }

    /// <summary>
    /// Rebuilds the focus ring from the active focus root. When a modal is open,
    /// focus is scoped to the dialog's widgets — Tab only cycles between dialog
    /// controls, not the main application's widgets. When no modal is open,
    /// focus returns to Root's subtree.
    /// </summary>
    private void RebuildFocusRing()
    {
        // Clear focus on the previously focused widget so it doesn't render
        // as focused after the ring changes.
        if (_focusRing.Count > 0 && _focusIndex < _focusRing.Count)
            _focusRing[_focusIndex].IsFocused = false;

        // Scope focus to the topmost modal, or fall back to Root.
        Widget focusRoot = _modalStack.Count > 0 ? _modalStack.Peek() : Root;
        _focusRing = CollectFocusable(focusRoot);
        _focusIndex = 0;

        if (_focusRing.Count > 0)
            _focusRing[0].IsFocused = true;
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
