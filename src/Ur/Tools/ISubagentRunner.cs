namespace Ur.Tools;

/// <summary>
/// Contract for spawning an isolated sub-agent that runs its own LLM–tool loop
/// and returns the final text response.
///
/// Lives in Ur.Tools alongside its sole consumer (SubagentTool) so the Tools
/// namespace has no dependency on Ur.AgentLoop. The concrete SubagentRunner
/// lives in Ur.AgentLoop and implements this interface — it already depends on
/// Ur.Tools for ToolRegistry, so the dependency is one-directional.
///
/// Keeping this thin — one method, one return type — ensures the boundary stays
/// clean. If we later need depth limits, model override, or token reporting, they
/// can be added here without changing SubagentTool.
/// </summary>
internal interface ISubagentRunner
{
    /// <summary>
    /// Runs the given <paramref name="task"/> in a new, isolated agent loop.
    /// Returns the last text produced by the sub-agent, or a fixed
    /// "no response" string if the sub-agent emitted only tool calls with
    /// no final text.
    /// </summary>
    Task<string> RunAsync(string task, CancellationToken ct);
}
