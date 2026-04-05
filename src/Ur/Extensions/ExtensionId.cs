namespace Ur.Extensions;

internal readonly record struct ExtensionId(ExtensionTier Tier, string Name)
{
    public override string ToString() =>
        $"{Tier.ToString().ToLowerInvariant()}:{Name}";

    public static ExtensionId Parse(string value) =>
        TryParse(value, out var extensionId)
            ? extensionId
            : throw new FormatException($"'{value}' is not a valid extension ID.");

    public static bool TryParse(string? value, out ExtensionId extensionId)
    {
        extensionId = default;

        if (string.IsNullOrWhiteSpace(value))
            return false;

        var separatorIndex = value.IndexOf(':');
        if (separatorIndex <= 0 || separatorIndex == value.Length - 1)
            return false;

        var tierText = value[..separatorIndex];
        var name = value[(separatorIndex + 1)..];
        if (!Enum.TryParse<ExtensionTier>(tierText, ignoreCase: true, out var tier))
            return false;

        extensionId = new ExtensionId(tier, name);
        return true;
    }
}
