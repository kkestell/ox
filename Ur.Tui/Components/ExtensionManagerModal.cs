using System.Text;
using Ur.Extensions;
using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Components;

internal enum ExtensionManagerActionKind
{
    SetEnabled,
    Reset,
}

internal readonly record struct ExtensionManagerAction(
    ExtensionManagerActionKind Kind,
    string ExtensionId,
    bool Enabled);

public sealed class ExtensionManagerModal : Widget
{
    public const int ModalWidth = 76;
    public const int ModalHeight = 18;

    // Layout: row 0 = title, row 1 = filter, row 2 = separator, rows 3..N-5 = list,
    // separator, then 3 detail rows, then footer row.
    private const int HeaderRows = 3;
    private const int DetailRows = 4;
    private const int FooterRows = 1;
    private const string MutationBlockedMessage = "Read-only while a turn is running.";

    private static readonly Color ModalBg = new(30, 30, 60);
    private static readonly Color TitleFg = new(255, 255, 100);
    private static readonly Color HintFg = new(128, 128, 128);
    private static readonly Color FilterFg = Color.White;
    private static readonly Color ItemFg = new(180, 180, 180);
    private static readonly Color SelectedFg = Color.White;
    private static readonly Color SelectedBg = new(60, 60, 120);
    private static readonly Color EnabledFg = new(120, 220, 120);
    private static readonly Color DisabledFg = new(220, 180, 120);
    private static readonly Color ErrorFg = new(255, 120, 120);
    private static readonly Color FooterErrorFg = new(255, 160, 160);
    private static readonly Color FooterOkFg = new(180, 220, 180);

    private readonly StringBuilder _filter = new();
    private List<UrExtensionInfo> _allExtensions;
    private List<UrExtensionInfo> _filtered;

    private readonly ScrollableList<UrExtensionInfo> _list;

    public ExtensionManagerModal(IReadOnlyList<UrExtensionInfo> extensions)
    {
        Border = true;
        BorderForeground = new Color(220, 220, 220);
        BorderBackground = ModalBg;
        Background = ModalBg;

        _allExtensions = SortExtensions(extensions).ToList();
        _filtered = _allExtensions.ToList();

        _list = new ScrollableList<UrExtensionInfo>
        {
            Background = ModalBg,
            ItemRenderer = RenderItem,
            Items = _filtered,
        };
    }

    public bool Dismissed { get; private set; }
    public bool IsMutationBlocked { get; set; }
    public bool IsAwaitingWorkspaceEnableConfirmation { get; private set; }
    internal ExtensionManagerAction? RequestedAction { get; private set; }
    public string Filter => _filter.ToString();
    public IReadOnlyList<UrExtensionInfo> FilteredExtensions => _filtered;
    public string? FeedbackMessage { get; private set; }
    public bool FeedbackIsError { get; private set; }
    public UrExtensionInfo? SelectedExtension => _list.SelectedItem;

    protected override void RenderContent(Buffer buffer, Rect area)
    {
        var bg = ModalBg;

        // Title
        buffer.WriteString(area.X, area.Y, "Extensions", TitleFg, bg);

        // Filter
        buffer.WriteString(area.X, area.Y + 1, "Filter: ", HintFg, bg);
        buffer.WriteString(area.X + 8, area.Y + 1, _filter.ToString(), FilterFg, bg);

        // Separator
        for (var x = 0; x < area.Width; x++)
            buffer.Set(area.X + x, area.Y + 2, new Cell('─', HintFg, bg));

        // List area
        var listHeight = area.Height - HeaderRows - DetailRows - FooterRows - 1; // -1 for detail separator
        if (listHeight > 0)
        {
            var listRect = new Rect(area.X, area.Y + HeaderRows, area.Width, listHeight);
            _list.Render(buffer, listRect);
        }

        // Detail separator
        var detailSepY = area.Y + HeaderRows + Math.Max(0, listHeight);
        for (var x = 0; x < area.Width; x++)
            buffer.Set(area.X + x, detailSepY, new Cell('─', HintFg, bg));

        // Detail area
        var detailTop = detailSepY + 1;
        if (SelectedExtension is { } selected)
        {
            buffer.WriteString(area.X, detailTop, selected.Name, TitleFg, bg);
            buffer.WriteString(area.X, detailTop + 1, selected.Description, HintFg, bg);
            buffer.WriteString(
                area.X,
                detailTop + 2,
                $"Version {selected.Version}  Tier {selected.Tier}  Default {(selected.DefaultEnabled ? "enabled" : "disabled")}",
                HintFg,
                bg);
        }
        else
        {
            buffer.WriteString(area.X, detailTop, "No extensions match the current filter.", HintFg, bg);
        }

        // Footer
        var footerY = area.Y + area.Height - 1;
        var footer = BuildFooterText();
        var footerColor = FeedbackMessage is null
            ? HintFg
            : FeedbackIsError ? FooterErrorFg : FooterOkFg;
        buffer.WriteString(area.X, footerY, footer, footerColor, bg);
    }

    public override bool HandleKey(KeyEvent key)
    {
        switch (key.Key)
        {
            case Key.Enter:
                return HandleEnter();

            case Key.Delete:
                return HandleReset();

            case Key.Escape:
                if (IsAwaitingWorkspaceEnableConfirmation)
                {
                    CancelConfirmation();
                    SetFeedback("Workspace enable canceled.", isError: false);
                    return true;
                }

                Dismissed = true;
                return false;

            case Key.Up or Key.Down or Key.Home or Key.End or Key.PageUp or Key.PageDown:
                CancelConfirmation();
                return _list.HandleKey(key);

            case Key.Backspace:
                CancelConfirmation();
                if (_filter.Length > 0)
                {
                    _filter.Remove(_filter.Length - 1, 1);
                    ApplyFilter();
                }
                return true;

            default:
                if (key.Char.HasValue)
                {
                    CancelConfirmation();
                    _filter.Append(key.Char.Value);
                    ApplyFilter();
                }
                return true;
        }
    }

    internal void ReplaceExtensions(IReadOnlyList<UrExtensionInfo> extensions)
    {
        var selectedId = SelectedExtension?.Id;
        _allExtensions = SortExtensions(extensions).ToList();
        ApplyFilter(selectedId);
    }

    internal void ClearRequestedAction() =>
        RequestedAction = null;

    internal void SetFeedback(string message, bool isError)
    {
        FeedbackMessage = message;
        FeedbackIsError = isError;
    }

    private bool HandleEnter()
    {
        if (SelectedExtension is null)
            return true;

        if (IsMutationBlocked)
        {
            SetFeedback(MutationBlockedMessage, isError: true);
            return true;
        }

        if (IsAwaitingWorkspaceEnableConfirmation)
        {
            RequestedAction = new ExtensionManagerAction(
                ExtensionManagerActionKind.SetEnabled,
                SelectedExtension.Id,
                Enabled: true);
            IsAwaitingWorkspaceEnableConfirmation = false;
            return false;
        }

        var nextEnabled = !SelectedExtension.DesiredEnabled;
        if (SelectedExtension.Tier is ExtensionTier.Workspace && nextEnabled)
        {
            IsAwaitingWorkspaceEnableConfirmation = true;
            SetFeedback(
                $"Press Enter again to enable workspace extension '{SelectedExtension.Name}'.",
                isError: false);
            return true;
        }

        RequestedAction = new ExtensionManagerAction(
            ExtensionManagerActionKind.SetEnabled,
            SelectedExtension.Id,
            nextEnabled);
        return false;
    }

    private bool HandleReset()
    {
        if (SelectedExtension is null || !SelectedExtension.HasOverride)
            return true;

        if (IsMutationBlocked)
        {
            SetFeedback(MutationBlockedMessage, isError: true);
            return true;
        }

        RequestedAction = new ExtensionManagerAction(
            ExtensionManagerActionKind.Reset,
            SelectedExtension.Id,
            Enabled: SelectedExtension.DefaultEnabled);
        return false;
    }

    private void ApplyFilter(string? preferredSelectionId = null)
    {
        var filterText = _filter.ToString();
        _filtered = _allExtensions
            .Where(extension =>
                filterText.Length == 0 ||
                extension.Name.Contains(filterText, StringComparison.OrdinalIgnoreCase) ||
                extension.Id.Contains(filterText, StringComparison.OrdinalIgnoreCase) ||
                extension.Description.Contains(filterText, StringComparison.OrdinalIgnoreCase))
            .ToList();

        _list.Items = _filtered;

        var selectedIndex = 0;
        if (preferredSelectionId is not null)
        {
            var index = _filtered.FindIndex(extension => extension.Id == preferredSelectionId);
            if (index >= 0)
                selectedIndex = index;
        }

        _list.SetSelectedIndex(selectedIndex);
    }

    private void CancelConfirmation() =>
        IsAwaitingWorkspaceEnableConfirmation = false;

    private string BuildFooterText()
    {
        if (FeedbackMessage is not null)
            return Truncate(FeedbackMessage, ModalWidth - 4);

        if (IsMutationBlocked)
            return MutationBlockedMessage;

        return IsAwaitingWorkspaceEnableConfirmation
            ? "Enter confirm  Esc cancel"
            : "Enter toggle  Del reset  Esc close";
    }

    private static IEnumerable<UrExtensionInfo> SortExtensions(IReadOnlyList<UrExtensionInfo> extensions) =>
        extensions
            .OrderBy(extension => TierOrder(extension.Tier))
            .ThenBy(extension => StatusOrder(extension))
            .ThenBy(extension => extension.Name, StringComparer.Ordinal);

    private static int TierOrder(ExtensionTier tier) =>
        tier switch
        {
            ExtensionTier.System => 0,
            ExtensionTier.User => 1,
            ExtensionTier.Workspace => 2,
            _ => 3,
        };

    private static int StatusOrder(UrExtensionInfo extension)
    {
        if (extension.IsActive)
            return 0;
        if (extension.LoadError is not null)
            return 1;
        return 2;
    }

    private static void RenderItem(Buffer buffer, Rect rect, UrExtensionInfo extension, bool isSelected)
    {
        var fg = isSelected ? SelectedFg : ItemFg;
        var bg = isSelected ? SelectedBg : ModalBg;

        if (isSelected)
            buffer.Fill(rect, new Cell(' ', fg, bg));

        var rowText = FormatRow(extension, rect.Width);
        buffer.WriteString(rect.X, rect.Y, rowText, fg, bg);
    }

    private static string FormatRow(UrExtensionInfo extension, int width)
    {
        var status = extension.LoadError is not null
            ? "error"
            : extension.DesiredEnabled
                ? extension.IsActive ? "enabled" : "pending"
                : "disabled";
        var tier = extension.Tier switch
        {
            ExtensionTier.System => "SYS",
            ExtensionTier.User => "USR",
            ExtensionTier.Workspace => "WRK",
            _ => "???",
        };

        return Truncate($"{tier}  {status,-8} {extension.Name}", width);
    }

    private static string Truncate(string value, int width)
    {
        if (value.Length <= width)
            return value;

        if (width <= 1)
            return value[..width];

        return value[..(width - 1)] + "…";
    }
}
