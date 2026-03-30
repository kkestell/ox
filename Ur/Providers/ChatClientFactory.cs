using System.ClientModel;
using Microsoft.Extensions.AI;
using OpenAI;

namespace Ur.Providers;

/// <summary>
/// Creates IChatClient instances for OpenRouter via the OpenAI SDK.
/// </summary>
internal static class ChatClientFactory
{
    private static readonly Uri OpenRouterEndpoint = new("https://openrouter.ai/api/v1");

    public static IChatClient Create(string modelId, string apiKey)
    {
        var options = new OpenAIClientOptions { Endpoint = OpenRouterEndpoint };

        return new OpenAI.Chat.ChatClient(
                modelId,
                new ApiKeyCredential(apiKey),
                options)
            .AsIChatClient();
    }
}
