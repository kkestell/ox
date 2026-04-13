using Microsoft.Extensions.AI;

namespace Ox.Agent.Tools;

/// <summary>
/// A factory that takes a fully-populated <see cref="ToolContext"/> and returns
/// a live <see cref="AIFunction"/> ready to be registered in a tool registry.
///
/// All tool registrations — builtins and future tool types — use this single
/// delegate shape. The registration loop has zero branching: build context once,
/// call every factory, register every result.
/// </summary>
internal delegate AIFunction ToolFactory(ToolContext context);
