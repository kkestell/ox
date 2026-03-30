using System.Collections.Concurrent;
using Ur.Terminal.Input;
using Ur.Terminal.Terminal;

namespace Ur.Terminal.Tests;

public sealed class KeyReaderTests
{
    [Fact]
    public void SplitEscapeSequence_IsBufferedUntilComplete()
    {
        using var terminal = new TestTerminal(
            [0x1B],
            [(byte)'[', (byte)'1', (byte)';', (byte)'5', (byte)'B']);

        using var cts = new CancellationTokenSource();
        var reader = new KeyReader(terminal);
        reader.Start(cts.Token);

        var keys = WaitForKeys(reader, expectedCount: 1);
        reader.Stop();

        var key = Assert.Single(keys);
        Assert.Equal(Key.Down, key.Key);
        Assert.Equal(Modifiers.Ctrl, key.Mods);
    }

    [Fact]
    public void BareEscape_IsFlushedAfterTimeoutRead()
    {
        using var terminal = new TestTerminal([0x1B]);

        using var cts = new CancellationTokenSource();
        var reader = new KeyReader(terminal);
        reader.Start(cts.Token);

        var keys = WaitForKeys(reader, expectedCount: 1);
        reader.Stop();

        var key = Assert.Single(keys);
        Assert.Equal(Key.Escape, key.Key);
    }

    [Fact]
    public void ModifierKeyEvents_EnrichFollowingKeyEvents()
    {
        using var terminal = new TestTerminal(
            [0x1B, 0x5B, 0x35, 0x37, 0x34, 0x34, 0x31, 0x3B, 0x32, 0x75],
            [0x0A],
            [0x1B, 0x5B, 0x31, 0x33, 0x3B, 0x32, 0x3A, 0x33, 0x75],
            [0x1B, 0x5B, 0x35, 0x37, 0x34, 0x34, 0x31, 0x3B, 0x31, 0x3A, 0x33, 0x75]);

        using var cts = new CancellationTokenSource();
        var reader = new KeyReader(terminal);
        reader.Start(cts.Token);

        var keys = WaitForKeys(reader, expectedCount: 2);
        reader.Stop();

        Assert.Collection(
            keys,
            key =>
            {
                Assert.Equal(Key.Enter, key.Key);
                Assert.Equal(Modifiers.Shift, key.Mods);
                Assert.Equal(KeyEventType.Press, key.EventType);
            },
            key =>
            {
                Assert.Equal(Key.Enter, key.Key);
                Assert.Equal(Modifiers.Shift, key.Mods);
                Assert.Equal(KeyEventType.Release, key.EventType);
            });
    }

    private static List<KeyEvent> WaitForKeys(KeyReader reader, int expectedCount)
    {
        var output = new List<KeyEvent>();
        var deadline = DateTime.UtcNow + TimeSpan.FromSeconds(1);

        while (DateTime.UtcNow < deadline)
        {
            reader.Drain(output);
            if (output.Count >= expectedCount)
                return output;

            Thread.Sleep(10);
        }

        reader.Drain(output);
        return output;
    }

    private sealed class TestTerminal(params byte[][] chunks) : ITerminal
    {
        private readonly ScriptedStream _stream = new(chunks);

        public int Width => 80;
        public int Height => 24;

        public void EnterRawMode()
        {
        }

        public void ExitRawMode()
        {
        }

        public void EnterAlternateBuffer()
        {
        }

        public void ExitAlternateBuffer()
        {
        }

        public void HideCursor()
        {
        }

        public void ShowCursor()
        {
        }

        public void SetCursorPosition(int x, int y)
        {
        }

        public void Write(ReadOnlySpan<byte> data)
        {
        }

        public Stream OpenInput() => _stream;

        public void Dispose()
        {
            _stream.Dispose();
        }
    }

    private sealed class ScriptedStream : Stream
    {
        private readonly ConcurrentQueue<byte[]> _chunks;

        public ScriptedStream(byte[][] chunks)
        {
            _chunks = new ConcurrentQueue<byte[]>(chunks);
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

        public override int Read(byte[] buffer, int offset, int count)
        {
            if (_chunks.TryDequeue(out var chunk))
            {
                chunk.CopyTo(buffer, offset);
                return chunk.Length;
            }

            Thread.Sleep(10);
            return 0;
        }

        public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
        public override void SetLength(long value) => throw new NotSupportedException();
        public override void Write(byte[] buffer, int offset, int count) => throw new NotSupportedException();
        public override void Flush()
        {
        }
    }
}
