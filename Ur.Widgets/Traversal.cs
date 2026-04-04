namespace Ur.Widgets;

/// <summary>
/// Provides tree traversal algorithms used by the layout engine.
/// Layout passes require both top-down (parent to children) and bottom-up (children to parent) traversals.
/// </summary>
internal static class Traversal
{
    /// <summary>
    /// Performs a breadth-first traversal, visiting each widget level by level.
    /// Used for top-down passes where parents process before children.
    /// </summary>
    public static void BreadthFirst(Widget root, Action<Widget> visit)
    {
        if (root is null) return;

        var queue = new Queue<Widget>();
        queue.Enqueue(root);

        while (queue.Count > 0)
        {
            var widget = queue.Dequeue();
            visit(widget);

            foreach (var child in widget.Children)
                queue.Enqueue(child);
        }
    }

    /// <summary>
    /// Performs a reverse breadth-first traversal, visiting deepest levels first.
    /// Used for bottom-up passes where children must be processed before parents.
    /// </summary>
    public static void ReverseBreadthFirst(Widget root, Action<Widget> visit)
    {
        if (root is null) return;

        var allWidgets = new List<Widget>();
        BreadthFirst(root, w => allWidgets.Add(w));

        for (var i = allWidgets.Count - 1; i >= 0; i--)
            visit(allWidgets[i]);
    }
}
