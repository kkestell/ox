using Ox.Terminal.Input;

namespace Ox.App;

/// <summary>
/// Writes a plain-text screen dump to the workspace's .ox/screen-dumps/ directory.
///
/// Two files are produced per capture: a timestamped archive file and a symlink
/// (or copy on platforms without symlink support) to latest.txt. The archive
/// path is returned so callers can log or display it.
///
/// Bound to Ctrl+G and F12 in the main loop — these two shortcuts are kept
/// deterministic because they're the fallback when terminal-native text
/// selection is unavailable during mouse-enabled TUI runs.
/// </summary>
public static class ScreenDumpWriter
{
    /// <summary>Ctrl+G key code — primary dump shortcut.</summary>
    public const int DumpKeyCode = (int)(KeyCode.G | KeyCode.CtrlMask);

    /// <summary>F12 key code — fallback dump shortcut.</summary>
    public const int DumpFallbackKeyCode = (int)KeyCode.F12;

    /// <summary>
    /// Returns true if <paramref name="keyCode"/> matches either dump shortcut.
    /// </summary>
    public static bool IsDumpShortcut(int keyCode) =>
        keyCode == DumpKeyCode || keyCode == DumpFallbackKeyCode;

    /// <summary>
    /// Write <paramref name="screenText"/> to the screen-dumps directory under
    /// <paramref name="workspacePath"/>. Creates the directory if it doesn't exist.
    /// Returns the absolute path to the timestamped archive file.
    /// </summary>
    public static string Write(string workspacePath, string screenText, DateTimeOffset now)
    {
        var dumpDir = Path.Combine(workspacePath, ".ox", "screen-dumps");
        Directory.CreateDirectory(dumpDir);

        // Timestamp uses the caller's local offset time (not UTC) so the
        // filename reflects the wall clock the user sees.
        var timestamp = now.ToString("yyyyMMdd-HHmmss-fff", System.Globalization.CultureInfo.InvariantCulture);
        var archiveFileName = $"screen-{timestamp}.txt";
        var archivePath = Path.Combine(dumpDir, archiveFileName);
        var latestPath = Path.Combine(dumpDir, "latest.txt");

        File.WriteAllText(archivePath, screenText);

        // Symlink latest.txt → archive. If symlinks aren't supported (or the
        // link already exists), fall back to a plain copy.
        try
        {
            if (File.Exists(latestPath) || IsSymlink(latestPath))
                File.Delete(latestPath);
            File.CreateSymbolicLink(latestPath, archivePath);
        }
        catch (IOException)
        {
            File.Copy(archivePath, latestPath, overwrite: true);
        }

        return archivePath;
    }

    private static bool IsSymlink(string path)
    {
        var info = new FileInfo(path);
        return info.Exists && info.LinkTarget is not null;
    }
}
