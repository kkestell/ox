using System.Text;

namespace Ox.Agent.Providers.OpenRouter;

/// <summary>
/// An HTTP <see cref="DelegatingHandler"/> that patches OpenRouter's reasoning-field
/// naming convention to match what the MEAI OpenAI adapter expects.
///
/// Root cause: OpenRouter normalizes all model reasoning into a unified
/// <c>"reasoning"</c> field (both in streaming SSE deltas and non-streaming
/// message objects). The MEAI <c>OpenAIChatClient</c> adapter (v10.4.1) probes
/// for <c>"reasoning_content"</c> — the field name used by the direct DeepSeek
/// API and vLLM. This mismatch means the adapter never finds the reasoning trace
/// and never emits <see cref="Microsoft.Extensions.AI.TextReasoningContent"/>.
///
/// Fix: rewrite every occurrence of <c>"reasoning":</c> to
/// <c>"reasoning_content":</c> in the HTTP response body before the SDK parses it.
/// After the rename the adapter's existing <c>TryGetReasoningDelta</c> and
/// <c>TryGetReasoningMessage</c> probes find the field and emit
/// <see cref="Microsoft.Extensions.AI.TextReasoningContent"/> items normally.
///
/// This handler is attached only to the <see cref="HttpClient"/> used by the
/// <see cref="OpenRouterProvider"/>, so it never fires for other providers. Two
/// code paths cover the two response modes:
///   - Non-streaming JSON: the body is buffered, the field name is substituted,
///     and the response content is replaced.
///   - Streaming SSE (Content-Type: text/event-stream): the response stream is
///     wrapped in a <see cref="ReasoningRenamingStream"/> that applies the
///     substitution line by line as the SDK reads each SSE event.
/// </summary>
internal sealed class OpenRouterReasoningHandler(HttpMessageHandler inner) : DelegatingHandler(inner)
{
    // The field name OpenRouter uses for reasoning traces in both streaming and non-streaming.
    internal const string Search = "\"reasoning\":";
    // The field name the MEAI OpenAI adapter expects.
    internal const string Replacement = "\"reasoning_content\":";

    protected override async Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request,
        CancellationToken cancellationToken)
    {
        var response = await base.SendAsync(request, cancellationToken);

        var mediaType = response.Content.Headers.ContentType?.MediaType;

        if (string.Equals(mediaType, "text/event-stream", StringComparison.OrdinalIgnoreCase))
        {
            // Streaming SSE: wrap the response stream with a line-by-line transformer.
            // We must not buffer the whole body — that would stall all output until
            // the model finishes generating, defeating the point of streaming.
            var originalStream = await response.Content.ReadAsStreamAsync(cancellationToken);
            var transformedStream = new ReasoningRenamingStream(originalStream, Search, Replacement);
            var newContent = new StreamContent(transformedStream);
            // Copy headers so Content-Type (with text/event-stream) is preserved.
            foreach (var (name, values) in response.Content.Headers)
                newContent.Headers.TryAddWithoutValidation(name, values);
            response.Content = newContent;
        }
        else
        {
            // Non-streaming JSON: buffer the full body, apply the substitution, and return.
            // The body is small enough to buffer — for non-streaming it fits in memory easily.
            var body = await response.Content.ReadAsStringAsync(cancellationToken);
            if (body.Contains(Search, StringComparison.Ordinal))
            {
                var modified = body.Replace(Search, Replacement, StringComparison.Ordinal);
                var charset = response.Content.Headers.ContentType?.CharSet;
                var encoding = charset is not null ? Encoding.GetEncoding(charset) : Encoding.UTF8;
                var newContent = new StringContent(modified, encoding, mediaType ?? "application/json");
                // Re-apply headers from the original content (StringContent already set Content-Type,
                // so we skip it to avoid the duplicate-header validation exception).
                foreach (var (name, values) in response.Content.Headers)
                {
                    if (!string.Equals(name, "Content-Type", StringComparison.OrdinalIgnoreCase))
                        newContent.Headers.TryAddWithoutValidation(name, values);
                }
                response.Content = newContent;
            }
        }

        return response;
    }
}

/// <summary>
/// Wraps a streaming HTTP response body and applies a text substitution on each
/// line before returning bytes to the caller.
///
/// Designed for Server-Sent Events (SSE) responses where the OpenAI SDK reads
/// the stream line by line. <see cref="StreamReader.ReadLineAsync"/> accumulates
/// partial TCP reads until a complete SSE line is available, so the substitution
/// target (<c>"reasoning":</c>) is never split across two calls to
/// <see cref="ReadAsync"/>. Empty lines — which separate SSE events — are
/// preserved: <see cref="StreamReader.ReadLineAsync"/> returns an empty string
/// for them, and we re-add the trailing newline so the SSE framing is intact.
/// </summary>
internal sealed class ReasoningRenamingStream : Stream
{
    private readonly StreamReader _reader;
    private readonly string _search;
    private readonly string _replacement;

    // Pending bytes from the most recently transformed line, buffered for
    // incremental delivery if the caller's buffer is smaller than one line.
    private byte[] _pending = [];
    private int _pendingOffset;

    // Set when ReadLineAsync returns null (EOF) so subsequent ReadAsync calls
    // return 0 without touching the reader again.
    private bool _done;

    public ReasoningRenamingStream(Stream inner, string search, string replacement)
    {
        // leaveOpen: false — we own the stream and dispose it with the reader.
        _reader = new StreamReader(inner, Encoding.UTF8,
            detectEncodingFromByteOrderMarks: false, leaveOpen: false);
        _search = search;
        _replacement = replacement;
    }

    public override bool CanRead => true;
    public override bool CanSeek => false;
    public override bool CanWrite => false;

    public override long Length => throw new NotSupportedException();
    public override long Position
    {
        get => throw new NotSupportedException();
        set => throw new NotSupportedException();
    }

    /// <summary>
    /// Reads transformed bytes into <paramref name="buffer"/>.
    ///
    /// First drains any leftover bytes from the previous line's transformation.
    /// Then reads the next complete SSE line, applies the substitution, encodes
    /// it back to UTF-8 (with the stripped newline re-appended), and drains as
    /// many bytes as the caller requested.
    /// </summary>
    public override async ValueTask<int> ReadAsync(Memory<byte> buffer, CancellationToken cancellationToken = default)
    {
        while (true)
        {
            // Drain pending bytes from the last transformed line.
            if (_pendingOffset < _pending.Length)
            {
                var n = Math.Min(buffer.Length, _pending.Length - _pendingOffset);
                _pending.AsSpan(_pendingOffset, n).CopyTo(buffer.Span);
                _pendingOffset += n;
                return n;
            }

            if (_done)
                return 0;

            // ReadLineAsync buffers internally — it waits for a complete line
            // before returning, so "reasoning": is never split across calls.
            // Returns null at EOF.
            var line = await _reader.ReadLineAsync(cancellationToken);
            if (line is null)
            {
                _done = true;
                return 0;
            }

            // Apply substitution and re-encode. ReadLineAsync strips the newline —
            // we add it back so SSE line framing is preserved for the OpenAI SDK parser.
            var transformed = line.Contains(_search, StringComparison.Ordinal)
                ? line.Replace(_search, _replacement, StringComparison.Ordinal) + "\n"
                : line + "\n";

            _pending = Encoding.UTF8.GetBytes(transformed);
            _pendingOffset = 0;
            // Loop to drain the freshly loaded pending bytes.
        }
    }

    public override Task<int> ReadAsync(byte[] buffer, int offset, int count, CancellationToken cancellationToken)
        => ReadAsync(buffer.AsMemory(offset, count), cancellationToken).AsTask();

    public override int Read(byte[] buffer, int offset, int count)
        => ReadAsync(buffer, offset, count, CancellationToken.None).GetAwaiter().GetResult();

    public override void Flush() { }

    public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
    public override void SetLength(long value) => throw new NotSupportedException();
    public override void Write(byte[] buffer, int offset, int count) => throw new NotSupportedException();

    protected override void Dispose(bool disposing)
    {
        if (disposing)
            _reader.Dispose();
        base.Dispose(disposing);
    }
}
