using System.Net;
using System.Net.Http.Headers;
using System.Text;
using Ur.Providers.OpenRouter;

namespace Ur.Tests.Providers;

/// <summary>
/// Unit tests for <see cref="OpenRouterReasoningHandler"/> and the inner
/// <see cref="ReasoningRenamingStream"/>.
///
/// The handler's job is to rename <c>"reasoning":</c> to
/// <c>"reasoning_content":</c> in HTTP responses from OpenRouter so the MEAI
/// OpenAI adapter can find and emit <see cref="Microsoft.Extensions.AI.TextReasoningContent"/>.
/// Tests exercise both code paths — non-streaming JSON and streaming SSE — and
/// confirm that responses without the pattern pass through unchanged.
/// </summary>
public sealed class OpenRouterReasoningHandlerTests
{
    // ─── Non-streaming (JSON) path ────────────────────────────────────────────

    [Fact]
    public async Task NonStreaming_WithReasoningField_RenamesField()
    {
        // A minimal non-streaming OpenRouter response containing "reasoning".
        var json = """{"choices":[{"message":{"content":"59","reasoning":"59 is prime"}}]}""";
        var handler = BuildHandler(json, "application/json");

        var result = await GetBodyAsync(handler);

        Assert.Contains("\"reasoning_content\":", result);
        Assert.DoesNotContain("\"reasoning\":", result);
        Assert.Contains("59 is prime", result);
    }

    [Fact]
    public async Task NonStreaming_WithoutReasoningField_PassesThroughUnchanged()
    {
        // A response with no "reasoning" field — handler should not touch it.
        var json = """{"choices":[{"message":{"content":"59"}}]}""";
        var handler = BuildHandler(json, "application/json");

        var result = await GetBodyAsync(handler);

        Assert.Equal(json, result);
    }

    [Fact]
    public async Task NonStreaming_MultipleReasoningOccurrences_RenamesAll()
    {
        // Unlikely but possible: multiple "reasoning": keys in one response.
        var json = """{"choices":[{"message":{"reasoning":"trace1"}},{"message":{"reasoning":"trace2"}}]}""";
        var handler = BuildHandler(json, "application/json");

        var result = await GetBodyAsync(handler);

        Assert.Equal(2, CountOccurrences(result, "\"reasoning_content\":"));
        Assert.DoesNotContain("\"reasoning\":", result);
    }

    // ─── Streaming SSE path ───────────────────────────────────────────────────

    [Fact]
    public async Task Streaming_WithReasoningDelta_RenamesField()
    {
        // A pair of SSE deltas — first with a "reasoning" field, second without.
        var sse = string.Join("\n",
            """data: {"choices":[{"delta":{"reasoning":"trace"}}]}""",
            "",
            """data: {"choices":[{"delta":{"content":"answer"}}]}""",
            "",
            "data: [DONE]",
            "");
        var handler = BuildHandler(sse, "text/event-stream");

        var result = await GetBodyAsync(handler);

        Assert.Contains("\"reasoning_content\":", result);
        Assert.DoesNotContain("\"reasoning\":", result);
        Assert.Contains("\"content\":\"answer\"", result);
    }

    [Fact]
    public async Task Streaming_WithoutReasoningDelta_PassesThroughUnchanged()
    {
        // SSE stream with no "reasoning" field — every line must be untouched.
        var sse = string.Join("\n",
            """data: {"choices":[{"delta":{"content":"hello"}}]}""",
            "",
            "data: [DONE]",
            "");
        var handler = BuildHandler(sse, "text/event-stream");

        var result = await GetBodyAsync(handler);

        Assert.Equal(sse, result);
    }

    [Fact]
    public async Task Streaming_MixedLines_OnlyRenamesMatchingLines()
    {
        // Three events: the first has "reasoning", the last two do not.
        var sse = string.Join("\n",
            """data: {"choices":[{"delta":{"reasoning":"think"}}]}""",
            "",
            """data: {"choices":[{"delta":{"content":"part1"}}]}""",
            "",
            """data: {"choices":[{"delta":{"content":"part2"}}]}""",
            "",
            "data: [DONE]",
            "");
        var handler = BuildHandler(sse, "text/event-stream");

        var result = await GetBodyAsync(handler);

        // Only the first data line should be rewritten.
        Assert.Contains("\"reasoning_content\":\"think\"", result);
        Assert.Contains("\"content\":\"part1\"", result);
        Assert.Contains("\"content\":\"part2\"", result);
        // No residual "reasoning": anywhere
        Assert.DoesNotContain("\"reasoning\":", result);
    }

    [Fact]
    public async Task Streaming_EmptyLinesPreserved()
    {
        // SSE events are separated by blank lines — these must survive transformation.
        var sse = "data: event1\n\ndata: event2\n\n";
        var handler = BuildHandler(sse, "text/event-stream");

        var result = await GetBodyAsync(handler);

        // Two blank lines separating three events must still be present.
        Assert.Equal(sse, result);
    }

    // ─── ReasoningRenamingStream read-size independence ───────────────────────

    [Fact]
    public async Task StreamTransform_SmallReadBuffer_DeliversAllBytes()
    {
        // Verifies that the stream delivers correct output even when callers read
        // one byte at a time — the typical concern with custom Stream subclasses.
        var sse = """data: {"reasoning":"r1"}\n\ndata: [DONE]\n""";
        var innerStream = new MemoryStream(Encoding.UTF8.GetBytes(sse));
        using var stream = new ReasoningRenamingStream(innerStream,
            OpenRouterReasoningHandler.Search,
            OpenRouterReasoningHandler.Replacement);

        var output = new MemoryStream();
        var oneByte = new byte[1];
        int read;
        while ((read = await stream.ReadAsync(oneByte)) > 0)
            output.Write(oneByte, 0, read);

        var result = Encoding.UTF8.GetString(output.ToArray());
        Assert.Contains("\"reasoning_content\":", result);
        Assert.DoesNotContain("\"reasoning\":", result);
    }

    [Fact]
    public async Task StreamTransform_LargeReadBuffer_DeliversAllBytes()
    {
        // Verifies that large reads (larger than the internal buffer) work correctly.
        var sse = string.Concat(
            Enumerable.Range(0, 50)
                .Select(i => $"data: {{\"choices\":[{{\"delta\":{{\"reasoning\":\"trace {i}\"}}}}]}}\n\n"));
        var innerStream = new MemoryStream(Encoding.UTF8.GetBytes(sse));
        using var stream = new ReasoningRenamingStream(innerStream,
            OpenRouterReasoningHandler.Search,
            OpenRouterReasoningHandler.Replacement);

        using var output = new MemoryStream();
        var buf = new byte[8192];
        int n;
        while ((n = await stream.ReadAsync(buf)) > 0)
            output.Write(buf, 0, n);

        var result = Encoding.UTF8.GetString(output.ToArray());
        Assert.Equal(50, CountOccurrences(result, "\"reasoning_content\":"));
        Assert.DoesNotContain("\"reasoning\":", result);
    }

    // ─── Helpers ─────────────────────────────────────────────────────────────

    /// <summary>
    /// Builds an <see cref="OpenRouterReasoningHandler"/> backed by a stub that
    /// always returns <paramref name="body"/> with the given <paramref name="mediaType"/>.
    /// </summary>
    private static OpenRouterReasoningHandler BuildHandler(string body, string mediaType)
    {
        var stub = new StubHandler(body, mediaType);
        return new OpenRouterReasoningHandler(stub);
    }

    /// <summary>
    /// Sends a dummy GET through the handler and reads the full response body.
    /// </summary>
    private static async Task<string> GetBodyAsync(HttpMessageHandler handler)
    {
        using var client = new HttpClient(handler);
        using var response = await client.GetAsync("https://openrouter.ai/api/v1/test");
        return await response.Content.ReadAsStringAsync();
    }

    private static int CountOccurrences(string source, string pattern)
    {
        int count = 0, index = 0;
        while ((index = source.IndexOf(pattern, index, StringComparison.Ordinal)) >= 0)
        {
            count++;
            index += pattern.Length;
        }
        return count;
    }

    /// <summary>
    /// A minimal <see cref="HttpMessageHandler"/> that always returns a pre-built
    /// response regardless of the request.
    /// </summary>
    private sealed class StubHandler(string body, string mediaType) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            var content = new StringContent(body, Encoding.UTF8);
            content.Headers.ContentType = new MediaTypeHeaderValue(mediaType)
            {
                CharSet = "utf-8"
            };
            var response = new HttpResponseMessage(HttpStatusCode.OK) { Content = content };
            return Task.FromResult(response);
        }
    }
}
