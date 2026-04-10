using Terminal.Gui.Drivers;
using Terminal.Gui.Input;

namespace Ox;

/// <summary>
/// Writes a point-in-time textual snapshot of the running TUI to disk.
///
/// Mouse reporting and alternate-screen mode make terminal-native selection
/// unreliable while Ox is running, so the dump path provides a deterministic
/// way to capture rendering bugs without depending on terminal clipboard tools.
/// </summary>
internal static class ScreenDumpWriter
{
    private const string DumpDirectoryName = ".ox/screen-dumps";
    private const string LatestFileName = "latest.txt";
    internal const int DumpKeyCode = (int)KeyCode.F12;
    internal const int DumpFallbackKeyCode = (int)(KeyCode.G | KeyCode.CtrlMask);

    public static bool IsDumpShortcut(int keyCode) =>
        keyCode == DumpKeyCode || keyCode == DumpFallbackKeyCode;

    public static string Write(string workspacePath, string screenText, DateTimeOffset now)
    {
        var dumpDirectory = Path.Combine(workspacePath, DumpDirectoryName);
        Directory.CreateDirectory(dumpDirectory);

        var archivePath = BuildArchivePath(workspacePath, now);
        var latestPath = Path.Combine(dumpDirectory, LatestFileName);

        File.WriteAllText(archivePath, screenText);
        File.WriteAllText(latestPath, screenText);

        return archivePath;
    }

    public static string BuildArchivePath(string workspacePath, DateTimeOffset now) =>
        Path.Combine(
            workspacePath,
            DumpDirectoryName,
            $"screen-{now:yyyyMMdd-HHmmss-fff}.txt");
}
