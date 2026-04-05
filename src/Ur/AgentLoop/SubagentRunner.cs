using Microsoft.Extensions.AI;
using Ur.Permissions;
using Ur.Tools;

namespace Ur.AgentLoop;

/// <summary>
/// Runs a sub-agent in a fully isolated message history, then returns the
/// final text response. This is the concrete implementation of
/// <see cref="ISubagentRunner"/> — the interface lives in the same namespace
/// to avoid exposing it publicly, while the concrete class can reference
/// AgentLoop directly.
///
/// Architecture: SubagentRunner mirrors what <see cref="Ur.Sessions.UrSession"/>
/// does in RunTurnAsync — build a message list, create an AgentLoop, run to
/// termination — but without session persistence. The sub-agent gets:
///   - A fresh empty message history (fully isolated from the parent turn).
///   - A filtered copy of the parent's tool registry (run_subagent excluded to
///     prevent unbounded recursion — simpler and safer than a depth counter).
///   - The same wrapped TurnCallbacks as the parent, so the user is not re-prompted
///     for grants they've already approved in this session.
///   - The same system prompt as the parent turn.
/// </summary>
internal sealed class SubagentRunner(
    IChatClient client,
    ToolRegistry parentTools,
    Workspace workspace,
    TurnCallbacks? callbacks,
    string? systemPrompt) : ISubagentRunner
{
    /// <summary>
    /// Runs <paramref name="task"/> as the sole user message in a fresh agent loop.
    /// Collects all ResponseChunk text produced across the loop's iterations and
    /// returns the accumulated result when the loop terminates (TurnCompleted or Error).
    ///
    /// Returns a fixed "no response" string if the sub-agent emitted only tool calls
    /// with no final text — this makes the tool result always well-defined.
    ///
    /// Throws <see cref="InvalidOperationException"/> when the inner agent loop reports
    /// a fatal error (e.g., LLM API failure) so the parent's ToolInvoker surfaces an
    /// actionable error result instead of a misleading empty-response string.
    /// </summary>
    public async Task<string> RunAsync(string task, CancellationToken ct)
    {
        // Filtered copy omits run_subagent from the child registry, blocking direct
        // self-recursion. Using SubagentTool.ToolName (rather than a local constant
        // copy) keeps the two sides of this contract in sync — Ur.Tools is already
        // imported via ToolRegistry.
        var subAgentTools = parentTools.FilteredCopy(SubagentTool.ToolName);

        var messages = new List<ChatMessage>
        {
            new(ChatRole.User, task)
        };

        var agentLoop = new AgentLoop(client, subAgentTools, workspace);

        // Accumulate all response text across the loop iterations. The loop may
        // call tools and loop back multiple times before producing a final text.
        var responseText = new System.Text.StringBuilder();

        await foreach (var loopEvent in agentLoop.RunTurnAsync(messages, callbacks, systemPrompt, ct))
        {
            switch (loopEvent)
            {
                case ResponseChunk chunk:
                    responseText.Append(chunk.Text);
                    break;

                case Error { IsFatal: true } fatalError:
                    // A fatal error (e.g., LLM API failure) must propagate — absorbing it
                    // would make an unrecoverable failure indistinguishable from a silent
                    // no-text response. ToolInvoker wraps this in an error tool result.
                    throw new InvalidOperationException(
                        $"Sub-agent failed: {fatalError.Message}");

                // Non-fatal errors and TurnCompleted are terminal; the loop will not
                // yield further. Let the foreach complete naturally.
                case TurnCompleted:
                case Error:
                    break;
            }
        }

        // An empty result means the model produced only tool calls with no prose.
        var result = responseText.ToString();
        return string.IsNullOrWhiteSpace(result)
            ? "(Sub-agent produced no text response.)"
            : result;
    }
}
