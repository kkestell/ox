using System.Text.Json;
using Lua;

namespace Ur.Extensions;

/// <summary>
/// Translates a Lua manifest table into a C# <see cref="ExtensionDescriptor"/>.
///
/// Separated from <see cref="ExtensionLoader"/> so that the loader handles Lua I/O
/// (reading files, evaluating scripts) while this class handles the pure data mapping
/// from <see cref="LuaTable"/> to domain objects. No file system or Lua runtime
/// interaction happens here.
/// </summary>
internal static class ManifestParser
{
    /// <summary>
    /// Reads the required and optional fields from a manifest <see cref="LuaTable"/>
    /// and builds an <see cref="Extension"/> with the appropriate descriptor.
    /// </summary>
    /// <param name="manifest">The Lua table returned by evaluating manifest.lua.</param>
    /// <param name="extDir">Absolute path to the extension's directory.</param>
    /// <param name="tier">The trust tier this extension was discovered in.</param>
    /// <returns>A new <see cref="Extension"/> with its descriptor populated.</returns>
    internal static Extension FromLuaTable(LuaTable manifest, string extDir, ExtensionTier tier)
    {
        var name = LuaTableHelpers.ReadRequiredString(manifest, "name");
        var version = LuaTableHelpers.ReadRequiredString(manifest, "version");
        var description = LuaTableHelpers.ReadOptionalString(manifest, "description") ?? "";
        var settingsSchemas = ParseSettingsSchemas(manifest);

        return new Extension(
            new ExtensionDescriptor(
                new ExtensionId(tier, name),
                description,
                version,
                extDir,
                settingsSchemas));
    }

    /// <summary>
    /// Extracts settings schemas from the manifest's "settings" table, if present.
    /// Each key maps to a JSON schema (represented as a <see cref="JsonElement"/>)
    /// that describes the setting's type and constraints.
    /// </summary>
    private static Dictionary<string, JsonElement> ParseSettingsSchemas(LuaTable manifest)
    {
        var schemas = new Dictionary<string, JsonElement>(StringComparer.Ordinal);

        if (!manifest["settings"].TryRead<LuaTable>(out var settingsTable))
            return schemas;

        foreach (var (key, value) in settingsTable)
            if (key.TryRead<string>(out var settingKey) &&
                value.TryRead<LuaTable>(out var schemaTable))
                schemas[settingKey] = LuaJsonHelpers.ToJsonElement(schemaTable);

        return schemas;
    }

}
