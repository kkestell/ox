using System.Globalization;
using System.Text;

namespace Ur.Logging;

/// <summary>
/// Minimal append-only file logger that writes to ~/.ur/logs/ur-{date}.log.
///
/// Design goals:
///   - Zero dependencies: plain File.AppendAllText so it works even when the
///     rest of the stack is in a broken state (e.g., during an unhandled crash).
///   - Fire-and-forget: every write is wrapped in try/catch so a logging failure
///     never propagates to the caller or causes a secondary crash.
///   - Daily rolling: one file per day keeps the log directory tidy and makes it
///     easy to correlate a crash with "today's" file.
///   - Thread-safe: a single static lock guards all file writes.
/// </summary>
public static class UrLogger
{
    private static readonly string LogDir = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
        ".ur", "logs");

    private static readonly Lock WriteLock = new();

    // -------------------------------------------------------------------------
    // Public API
    // -------------------------------------------------------------------------

    /// <summary>Writes an informational message.</summary>
    public static void Info(string message) =>
        Append("INFO ", message);

    /// <summary>Writes an error message.</summary>
    public static void Error(string message) =>
        Append("ERROR", message);

    /// <summary>
    /// Writes an error message followed by the full exception chain
    /// (type, message, and stack trace for each inner exception level).
    /// </summary>
    public static void Exception(string context, Exception ex)
    {
        var sb = new StringBuilder();
        sb.AppendLine(FormatEntry("ERROR", context));

        var current = ex;
        var depth = 0;
        while (current is not null)
        {
            if (depth > 0)
                sb.AppendLine("  -- caused by --");

            sb.AppendLine(CultureInfo.InvariantCulture, $"  {current.GetType().FullName}: {current.Message}");

            if (current.StackTrace is { } stack)
            {
                foreach (var line in stack.Split('\n'))
                    sb.AppendLine(CultureInfo.InvariantCulture, $"    {line.TrimEnd()}");
            }

            current = current.InnerException;
            depth++;
        }

        AppendRaw(sb.ToString());
    }

    // -------------------------------------------------------------------------
    // Internals
    // -------------------------------------------------------------------------

    private static void Append(string level, string message) =>
        AppendRaw(FormatEntry(level, message) + Environment.NewLine);

    private static string FormatEntry(string level, string message) =>
        $"[{DateTime.Now:yyyy-MM-dd HH:mm:ss.fff}] [{level}] {message}";

    private static void AppendRaw(string text)
    {
        try
        {
            lock (WriteLock)
            {
                Directory.CreateDirectory(LogDir);
                var path = Path.Combine(LogDir, $"ur-{DateTime.Now:yyyy-MM-dd}.log");
                File.AppendAllText(path, text);
            }
        }
        catch
        {
            // Logging must never crash the application.
        }
    }
}
