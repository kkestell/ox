namespace Ur.Todo;

/// <summary>
/// Session-scoped, in-memory store for the current todo list.
///
/// The LLM writes to this via <c>TodoWriteTool</c> from the agent loop's async
/// pipeline, and the TUI sidebar reads it on a ThreadPool thread for rendering.
/// Thread safety is achieved via a lock around the item list — the write path
/// (full replacement) and read path (snapshot copy) are both fast enough that
/// contention is negligible.
///
/// Observable: the <see cref="Changed"/> event lets the sidebar react to updates
/// without polling. Subscribers must be safe to call from any thread.
/// </summary>
public sealed class TodoStore
{
    private readonly object _lock = new();
    private IReadOnlyList<TodoItem> _items = [];

    /// <summary>
    /// The current todo items. Returns a snapshot — safe to enumerate without
    /// holding the lock.
    /// </summary>
    public IReadOnlyList<TodoItem> Items
    {
        get
        {
            lock (_lock)
                return _items;
        }
    }

    /// <summary>
    /// Raised after the item list is replaced. Fired outside the lock to avoid
    /// deadlocks if a subscriber triggers a render that reads <see cref="Items"/>.
    /// </summary>
    public event Action? Changed;

    /// <summary>
    /// Atomically replaces the entire todo list. The LLM always sends a full
    /// replacement — no add/remove/patch semantics.
    /// </summary>
    public void Update(IReadOnlyList<TodoItem> items)
    {
        lock (_lock)
            _items = items;

        Changed?.Invoke();
    }
}
