using System.Collections.Concurrent;
using Microsoft.Extensions.Logging;

namespace Ur.Logging;

/// <summary>
/// Custom <see cref="ILoggerProvider"/> that writes to <c>~/.ur/logs/ur-{date}.log</c>.
///
/// Replaces the static <c>UrLogger</c> class. The daily-rolling, thread-safe,
/// fire-and-forget behaviour is identical — just wrapped in the standard
/// <see cref="ILogger"/> / <see cref="ILoggerProvider"/> abstractions so the DI
/// container can wire logging without a static dependency.
/// </summary>
internal sealed class UrFileLoggerProvider : ILoggerProvider
{
    private readonly string _logDir = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
        ".ur", "logs");

    private readonly ConcurrentDictionary<string, UrFileLogger> _loggers = new(StringComparer.Ordinal);

    public ILogger CreateLogger(string categoryName) =>
        _loggers.GetOrAdd(categoryName, name => new UrFileLogger(name, _logDir));

    public void Dispose() => _loggers.Clear();
}
