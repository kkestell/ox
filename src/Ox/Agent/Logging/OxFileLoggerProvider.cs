using System.Collections.Concurrent;
using Microsoft.Extensions.Logging;

namespace Ox.Agent.Logging;

/// <summary>
/// Custom <see cref="ILoggerProvider"/> that writes to <c>~/.ox/logs/ox-{date}.log</c>.
///
/// Daily-rolling, thread-safe, fire-and-forget file logging wrapped in the standard
/// <see cref="ILogger"/> / <see cref="ILoggerProvider"/> abstractions so the DI
/// container can wire logging without a static dependency.
/// </summary>
internal sealed class OxFileLoggerProvider : ILoggerProvider
{
    private readonly string _logDir = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
        ".ox", "logs");

    private readonly ConcurrentDictionary<string, OxFileLogger> _loggers = new(StringComparer.Ordinal);

    public ILogger CreateLogger(string categoryName) =>
        _loggers.GetOrAdd(categoryName, name => new OxFileLogger(name, _logDir));

    public void Dispose() => _loggers.Clear();
}
