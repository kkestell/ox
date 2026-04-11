namespace Ox.Views;

/// <summary>
/// A single fragment of laid-out text carrying its content and a style tag.
///
/// The generic parameter <typeparamref name="TStyle"/> lets the layout engine
/// stay independent of any specific rendering framework: production code uses
/// a Te-based style type, while tests use plain strings.
/// </summary>
/// <param name="Text">The text content of this fragment.</param>
/// <param name="Style">An opaque style tag carried through layout unchanged.</param>
public sealed record LayoutFragment<TStyle>(string Text, TStyle Style);
