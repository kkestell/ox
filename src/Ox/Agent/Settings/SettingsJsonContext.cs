using System.Text.Json;
using System.Text.Json.Serialization;

namespace Ox.Agent.Settings;

/// <summary>
/// Source-generated JSON serialization context for AoT-safe settings persistence.
/// Used by <see cref="SettingsWriter"/>, <see cref="Ox.Agent.Configuration.OxSettingsConfigurationProvider"/>,
/// and <see cref="Ox.Agent.Configuration.OxConfiguration"/> for typed setting serialization.
/// </summary>
[JsonSerializable(typeof(Dictionary<string, JsonElement>))]
[JsonSerializable(typeof(string))]
[JsonSerializable(typeof(bool))]
internal partial class SettingsJsonContext : JsonSerializerContext;
