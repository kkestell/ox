namespace Ox.Tests;

/// <summary>
/// Regression tests for Ox's screen-dump escape hatch.
///
/// The shortcut needs to stay deterministic because it is the fallback when
/// terminal-native text selection is unavailable during mouse-enabled TUI runs.
/// </summary>
public sealed class ScreenDumpWriterTests : IDisposable
{
    private readonly string _workspacePath = Path.Combine(
        Path.GetTempPath(),
        "ox-screen-dump-tests",
        Guid.NewGuid().ToString("N"));

    [Theory]
    [InlineData(Ox.ScreenDumpWriter.DumpKeyCode, true)]
    [InlineData(Ox.ScreenDumpWriter.DumpFallbackKeyCode, true)]
    [InlineData(71, false)]
    public void IsDumpShortcut_RecognizesSupportedKeys(int keyCode, bool expected)
    {
        var isShortcut = Ox.ScreenDumpWriter.IsDumpShortcut(keyCode);

        Assert.Equal(expected, isShortcut);
    }

    [Fact]
    public void Write_CreatesArchiveAndLatestFiles()
    {
        var now = new DateTimeOffset(2026, 4, 10, 9, 15, 30, TimeSpan.FromHours(-5));
        const string screenText = "line 1\nline 2";

        var archivePath = Ox.ScreenDumpWriter.Write(_workspacePath, screenText, now);
        var latestPath = Path.Combine(_workspacePath, ".ox", "screen-dumps", "latest.txt");

        Assert.Equal(
            Path.Combine(_workspacePath, ".ox", "screen-dumps", "screen-20260410-091530-000.txt"),
            archivePath);
        Assert.Equal(screenText, File.ReadAllText(archivePath));
        Assert.Equal(screenText, File.ReadAllText(latestPath));
    }

    public void Dispose()
    {
        if (Directory.Exists(_workspacePath))
            Directory.Delete(_workspacePath, recursive: true);
    }
}
