namespace Te.Input;

/// <summary>
/// Lightweight coordinate type for input events.
/// Te only needs integer terminal coordinates, so a tiny local type avoids
/// pulling in drawing primitives that imply a larger UI framework.
/// </summary>
public readonly record struct Point(int X, int Y);
