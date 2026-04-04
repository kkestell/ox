namespace Ur.Demo;

/// <summary>
/// Discriminated union of chat message types.
/// Each subtype carries the data its widget needs to render itself.
/// Using abstract records gives value equality for free, which makes
/// testing and pattern matching clean.
/// </summary>
public abstract record ChatMessage(string Content);

/// <summary>A message typed by a named user.</summary>
public record UserMessage(string Author, string Content) : ChatMessage(Content);

/// <summary>A system-level notification (joins, leaves, status changes).</summary>
public record SystemMessage(string Content) : ChatMessage(Content);

/// <summary>The result or invocation of a tool call.</summary>
public record ToolMessage(string ToolName, string Content) : ChatMessage(Content);
