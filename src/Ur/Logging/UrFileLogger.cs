using System.Globalization;
using System.Text;
using Microsoft.Extensions.Logging;

namespace Ur.Logging;

/// <summary>
/// Writes log entries to <c>~/.ur/logs/ur-{date}.log</c>. Daily-rolling, thread-safe,
/// fire-and-forget file logger.
///
/// A single static lock guards all file writes across all logger instances — the
/// underlying file is shared (one file per day, not per category). This prevents
/// interleaved or corrupted entries when multiple categories log simultaneously.
/// </summary>
internal sealed class UrFileLogger(string categoryName, string logDir) : ILogger
{
    private static readonly Lock WriteLock = new();

    public IDisposable? BeginScope<TState>(TState state) where TState : notnull => null;

    public bool IsEnabled(LogLevel logLevel) => logLevel >= LogLevel.Debug;

    public void Log<TState>(
        LogLevel logLevel,
        EventId eventId,
        TState state,
        Exception? exception,
        Func<TState, Exception?, string> formatter)
    {
        if (!IsEnabled(logLevel))
            return;

        var level = logLevel switch
        {
            LogLevel.Debug => "DEBUG",
            LogLevel.Information => "INFO ",
            LogLevel.Warning => "WARN ",
            LogLevel.Error => "ERROR",
            LogLevel.Critical => "FATAL",
            _ => logLevel.ToString().ToUpperInvariant()
        };

        var sb = new StringBuilder();
        sb.AppendLine(CultureInfo.InvariantCulture,
            $"[{DateTime.Now:yyyy-MM-dd HH:mm:ss.fff}] [{level}] [{categoryName}] {formatter(state, null)}");

        // Write the full exception chain (type, message, and stack trace per level)
        // when an exception is provided.
        var current = exception;
        var depth = 0;
        while (current is not null)
        {
            if (depth > 0)
                sb.AppendLine("  -- caused by --");

            sb.AppendLine(CultureInfo.InvariantCulture,
                $"  {current.GetType().FullName}: {current.Message}");

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

    private void AppendRaw(string text)
    {
        try
        {
            lock (WriteLock)
            {
                Directory.CreateDirectory(logDir);
                var path = Path.Combine(logDir, $"ur-{DateTime.Now:yyyy-MM-dd}.log");
                File.AppendAllText(path, text);
            }
        }
        catch
        {
            // Logging must never crash the application.
        }
    }
}
