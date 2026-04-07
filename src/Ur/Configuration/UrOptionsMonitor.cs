using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Options;

namespace Ur.Configuration;

/// <summary>
/// Lightweight <see cref="IOptionsMonitor{UrOptions}"/> implementation that reads
/// from <see cref="IConfiguration"/> on every access. This avoids pulling in the
/// full M.E.Options hosting machinery while still giving consumers a reactive view
/// of the current configuration.
///
/// <see cref="CurrentValue"/> re-binds from IConfiguration each time it is accessed,
/// so it always reflects the latest state after an <see cref="IConfigurationRoot.Reload"/>.
/// Change tokens are not wired — callers that need push notifications should use
/// <see cref="IConfigurationRoot"/> directly. In practice, Ur reads SelectedModelId
/// on each turn, so polling is sufficient.
/// </summary>
internal sealed class UrOptionsMonitor(IConfiguration configuration) : IOptionsMonitor<UrOptions>
{
    /// <summary>
    /// Returns a fresh <see cref="UrOptions"/> bound to the current IConfiguration
    /// state. Re-reads on every call so it always reflects post-reload values.
    /// </summary>
    public UrOptions CurrentValue => Get(Options.DefaultName);

    public UrOptions Get(string? name)
    {
        // Bind the "ur" section manually rather than using the full Options hosting
        // pipeline (IConfigureOptions<T>, IPostConfigureOptions<T>, etc.). This keeps
        // AddUr() self-contained and AoT-friendly. If UrOptions gains more properties,
        // add bindings here to match.
        var section = configuration.GetSection("ur");
        return new UrOptions { Model = section["model"] };
    }

    public IDisposable? OnChange(Action<UrOptions, string?> listener)
    {
        // Change notifications are not needed — Ur reads SelectedModelId
        // fresh on each turn via CurrentValue. Return null (callers must
        // handle a null return, per the interface contract).
        return null;
    }
}
