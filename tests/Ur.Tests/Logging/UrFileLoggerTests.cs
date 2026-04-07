using Microsoft.Extensions.Logging;
using Ur.Logging;

namespace Ur.Tests.Logging;

public sealed class UrFileLoggerTests : IDisposable
{
    private readonly string _logDir = Path.Combine(
        Path.GetTempPath(),
        "ur-logger-tests",
        Guid.NewGuid().ToString("N"));

    public void Dispose()
    {
        if (Directory.Exists(_logDir))
            Directory.Delete(_logDir, recursive: true);
    }

    private string TodayLogPath =>
        Path.Combine(_logDir, $"ur-{DateTime.Now:yyyy-MM-dd}.log");

    // ─── Level filtering ─────────────────────────────────────────────

    [Theory]
    [InlineData(LogLevel.Trace, false)]
    [InlineData(LogLevel.Debug, true)]
    [InlineData(LogLevel.Information, true)]
    [InlineData(LogLevel.Warning, true)]
    [InlineData(LogLevel.Error, true)]
    [InlineData(LogLevel.Critical, true)]
    public void IsEnabled_FiltersLevelsBelowDebug(LogLevel level, bool expected)
    {
        var logger = new UrFileLogger("Test", _logDir);

        Assert.Equal(expected, logger.IsEnabled(level));
    }

    // ─── Basic message format ────────────────────────────────────────

    [Fact]
    public void Log_WritesFormattedEntryWithTimestampAndCategory()
    {
        var logger = new UrFileLogger("MyApp.Service", _logDir);

        logger.LogInformation("Server started on port {Port}", 8080);

        var content = File.ReadAllText(TodayLogPath);
        // Verify the log entry contains the expected components.
        Assert.Contains("[INFO ]", content);
        Assert.Contains("[MyApp.Service]", content);
        Assert.Contains("Server started on port 8080", content);
    }

    // ─── Exception chain formatting ──────────────────────────────────

    [Fact]
    public void Log_WithException_WritesFullExceptionChain()
    {
        var logger = new UrFileLogger("Test", _logDir);

        var inner = new InvalidOperationException("root cause");
        var outer = new ApplicationException("wrapper", inner);

        logger.LogError(outer, "Operation failed");

        var content = File.ReadAllText(TodayLogPath);

        // Outer exception type and message.
        Assert.Contains("System.ApplicationException: wrapper", content);
        // Inner exception separator and content.
        Assert.Contains("-- caused by --", content);
        Assert.Contains("System.InvalidOperationException: root cause", content);
        // The log message itself.
        Assert.Contains("Operation failed", content);
    }

    // ─── Disabled levels produce no output ───────────────────────────

    [Fact]
    public void Log_AtDisabledLevel_WritesNothing()
    {
        var logger = new UrFileLogger("Test", _logDir);

        logger.LogTrace("This should not appear");

        Assert.False(File.Exists(TodayLogPath));
    }

    // ─── Provider creates loggers by category ────────────────────────

    [Fact]
    public void Provider_CreateLogger_ReturnsSameInstancePerCategory()
    {
        using var provider = new UrFileLoggerProvider();

        var logger1 = provider.CreateLogger("Category.A");
        var logger2 = provider.CreateLogger("Category.A");
        var logger3 = provider.CreateLogger("Category.B");

        Assert.Same(logger1, logger2);
        Assert.NotSame(logger1, logger3);
    }
}
