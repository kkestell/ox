namespace Ur.Tests;

/// <summary>
/// Regression test for the race condition where a second chat message cannot be
/// submitted. The bug: after a turn completes, SetTurnRunning(false) and
/// ReadLineAsync were dispatched as separate App.Invoke calls. Between the two
/// callbacks, _inputTcs was already completed from the first submission, so
/// OnTextFieldKeyDown's guard (line 217) silently dropped Enter presses.
///
/// This test models the race using the same TaskCompletionSource pattern that
/// InputAreaView uses internally.
/// </summary>
public sealed class InputSubmissionRaceTests
{
    /// <summary>
    /// Simulates the REPL loop's two-Invoke gap. When SetTurnRunning and
    /// ReadLineAsync are separate callbacks, an Enter press between them is
    /// lost because the old TCS is already completed and the new one hasn't
    /// been created yet.
    /// </summary>
    [Fact]
    public async Task SeparateInvokes_CanLoseSubmission()
    {
        // Simulate InputAreaView's _inputTcs field.
        TaskCompletionSource<string?>? inputTcs = null;

        // Simulate the guard in OnTextFieldKeyDown: returns false if no TCS
        // is active (null or already completed).
        bool TrySubmit(string text)
        {
            if (inputTcs is null || inputTcs.Task.IsCompleted)
                return false;
            return inputTcs.TrySetResult(text);
        }

        // --- First turn: works fine ---
        inputTcs = new TaskCompletionSource<string?>();
        Assert.True(TrySubmit("hello"));
        Assert.Equal("hello", await inputTcs.Task);

        // --- Gap between two separate Invokes ---
        // At this point, inputTcs.Task.IsCompleted == true.
        // SetTurnRunning(false) ran, but ReadLineAsync hasn't created a new TCS yet.
        // The user presses Enter during this gap.
        Assert.True(inputTcs.Task.IsCompleted);
        Assert.False(TrySubmit("second message")); // LOST!
    }

    /// <summary>
    /// When SetTurnRunning and ReadLineAsync happen in the same callback
    /// (the fix), there is no gap — the new TCS is created atomically,
    /// so the next Enter press is always captured.
    /// </summary>
    [Fact]
    public async Task AtomicInvoke_NeverLosesSubmission()
    {
        TaskCompletionSource<string?>? inputTcs = null;

        bool TrySubmit(string text)
        {
            if (inputTcs is null || inputTcs.Task.IsCompleted)
                return false;
            return inputTcs.TrySetResult(text);
        }

        // --- First turn ---
        inputTcs = new TaskCompletionSource<string?>();
        Assert.True(TrySubmit("hello"));
        Assert.Equal("hello", await inputTcs.Task);

        // --- Atomic callback: SetTurnRunning + ReadLineAsync in one Invoke ---
        // The new TCS is created immediately, no gap.
        inputTcs = new TaskCompletionSource<string?>();

        // The user can now submit immediately.
        Assert.True(TrySubmit("second message"));
        Assert.Equal("second message", await inputTcs.Task);
    }
}
