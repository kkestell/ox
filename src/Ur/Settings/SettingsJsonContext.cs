using System.Text.Json;
using System.Text.Json.Serialization;

namespace Ur.Settings;

/// <summary>
/// Source-generated JSON serialization context for AoT-safe settings persistence.
/// Used by <see cref="SettingsWriter"/>, <see cref="Ur.Configuration.UrSettingsConfigurationProvider"/>,
/// and <see cref="Ur.Configuration.UrConfiguration"/> for typed setting serialization.
/// </summary>
[JsonSerializable(typeof(Dictionary<string, JsonElement>))]
[JsonSerializable(typeof(string))]
[JsonSerializable(typeof(bool))]
internal partial class SettingsJsonContext : JsonSerializerContext;
