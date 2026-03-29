using Microsoft.Extensions.AI;

namespace Ur.Providers;

/// <summary>
/// Creates an IChatClient for a given provider and model.
/// Implemented by the host (CLI, GUI, etc.) which has the provider-specific packages.
/// </summary>
public interface IChatClientFactory
{
    IChatClient Create(string providerId, string modelId, string apiKey);
}
