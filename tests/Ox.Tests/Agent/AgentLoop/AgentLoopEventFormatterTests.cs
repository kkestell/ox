using Ox.Agent.AgentLoop;

namespace Ox.Tests.Agent.AgentLoop;

/// <summary>
/// Unit tests for <see cref="AgentLoopEventFormatter.TryFormatForStream"/>. These
/// pin the stream tags the headless runner writes to stderr ("[tool]", "[done]",
/// "[compacted]", ...) so a regression in the formatter would fail here instead
/// of showing up as visibly wrong CLI output in eval runs.
/// </summary>
public sealed class AgentLoopEventFormatterTests
{
    [Fact]
    public void ToolCallStarted_FormatsAsToolTag()
    {
        var evt = new ToolCallStarted
        {
            CallId = "c1",
            ToolName = "read_file",
            Arguments = new Dictionary<string, object?> { ["file_path"] = "x.cs" },
        };

        Assert.True(AgentLoopEventFormatter.TryFormatForStream(evt, "", out var line));
        Assert.StartsWith("[tool] ", line);
    }

    [Fact]
    public void ToolCallCompleted_SuccessFormatsAsToolOk()
    {
        var evt = new ToolCallCompleted { CallId = "c1", ToolName = "read_file", Result = "ok", IsError = false };
        Assert.True(AgentLoopEventFormatter.TryFormatForStream(evt, "", out var line));
        Assert.Contains("[tool-ok]", line);
        Assert.Contains("ok", line);
    }

    [Fact]
    public void ToolCallCompleted_ErrorFormatsAsToolErr()
    {
        var evt = new ToolCallCompleted { CallId = "c1", ToolName = "bash", Result = "boom", IsError = true };
        Assert.True(AgentLoopEventFormatter.TryFormatForStream(evt, "", out var line));
        Assert.Contains("[tool-err]", line);
    }

    [Fact]
    public void ToolCallCompleted_LongResultIsTruncated()
    {
        var longResult = new string('a', 500);
        var evt = new ToolCallCompleted { CallId = "c1", ToolName = "read_file", Result = longResult, IsError = false };
        Assert.True(AgentLoopEventFormatter.TryFormatForStream(evt, "", out var line));
        // Truncation ellipsis is a reliable marker; avoid coupling to the exact length constant.
        Assert.Contains("…", line);
    }

    [Fact]
    public void ToolAwaitingApproval_IncludesCallId()
    {
        var evt = new ToolAwaitingApproval { CallId = "call-42" };
        Assert.True(AgentLoopEventFormatter.TryFormatForStream(evt, "", out var line));
        Assert.Contains("[awaiting-approval]", line);
        Assert.Contains("call-42", line);
    }

    [Fact]
    public void TurnCompleted_WithTokens_IncludesCount()
    {
        var evt = new TurnCompleted { InputTokens = 1234 };
        Assert.True(AgentLoopEventFormatter.TryFormatForStream(evt, "", out var line));
        Assert.Contains("[done]", line);
        Assert.Contains("1234", line);
    }

    [Fact]
    public void TurnCompleted_WithoutTokens_OmitsCount()
    {
        var evt = new TurnCompleted();
        Assert.True(AgentLoopEventFormatter.TryFormatForStream(evt, "", out var line));
        Assert.Equal("[done]", line);
    }

    [Fact]
    public void Compacted_IncludesMessage()
    {
        var evt = new Compacted { Message = "summarized" };
        Assert.True(AgentLoopEventFormatter.TryFormatForStream(evt, "", out var line));
        Assert.Contains("[compacted]", line);
        Assert.Contains("summarized", line);
    }

    [Fact]
    public void Prefix_IsPrependedToTag()
    {
        var evt = new TurnCompleted();
        Assert.True(AgentLoopEventFormatter.TryFormatForStream(evt, "  [sub] ", out var line));
        Assert.StartsWith("  [sub] [done]", line);
    }

    [Fact]
    public void ThinkingChunk_NotFormattedHere()
    {
        // Thinking chunks are intentionally not handled — coalescing is stateful
        // and lives in HeadlessRunner. The formatter must report this as "false"
        // so the caller routes the event through its own branch.
        var evt = new ThinkingChunk { Text = "hmm" };
        Assert.False(AgentLoopEventFormatter.TryFormatForStream(evt, "", out _));
    }

    [Fact]
    public void SubagentEvent_NotFormattedHere()
    {
        // SubagentEvent is a relay envelope — the headless runner recurses on
        // evt.Inner with a "[sub] " prefix. The formatter doesn't know how to do
        // that, so it must report false.
        var evt = new SubagentEvent { SubagentId = "s1", Inner = new TurnCompleted() };
        Assert.False(AgentLoopEventFormatter.TryFormatForStream(evt, "", out _));
    }

    [Fact]
    public void ResponseChunk_NotFormattedHere()
    {
        // ResponseChunk is stdout-bound in the headless runner — the formatter
        // must not steal it from that routing.
        var evt = new ResponseChunk { Text = "hi" };
        Assert.False(AgentLoopEventFormatter.TryFormatForStream(evt, "", out _));
    }
}
