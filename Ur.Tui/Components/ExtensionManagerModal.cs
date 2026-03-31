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

public sealed class ExtensionManagerModal : IComponent
{
    public const int ModalWidth = 76;
    public const int ModalHeight = 18;

    private const int ListStartRow = 4;
    private const int DetailHeight = 4;
    private const string MutationBlockedMessage = "Read-only while a turn is running.";

    private static readonly Color BorderFg = new(220, 220, 220);
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
    private int _selectedIndex;
    private int _scrollOffset;

    public ExtensionManagerModal(IReadOnlyList<UrExtensionInfo> extensions)
    {
        _allExtensions = SortExtensions(extensions).ToList();
        _filtered = _allExtensions.ToList();
    }

    public bool Dismissed { get; private set; }
    public bool IsMutationBlocked { get; set; }
    public bool IsAwaitingWorkspaceEnableConfirmation { get; private set; }
    internal ExtensionManagerAction? RequestedAction { get; private set; }
    public string Filter => _filter.ToString();
    public IReadOnlyList<UrExtensionInfo> FilteredExtensions => _filtered;
    public string? FeedbackMessage { get; private set; }
    public bool FeedbackIsError { get; private set; }
    public UrExtensionInfo? SelectedExtension =>
        _selectedIndex >= 0 && _selectedIndex < _filtered.Count ? _filtered[_selectedIndex] : null;

    public void Render(Buffer buffer, Rect area)
    {
        var mx = (area.Width - ModalWidth) / 2 + area.X;
        var my = (area.Height - ModalHeight) / 2 + area.Y;
        var modalRect = new Rect(mx, my, ModalWidth, ModalHeight);
        var listHeight = ModalHeight - ListStartRow - DetailHeight - 2;

        buffer.Fill(modalRect, new Cell(' ', BorderFg, ModalBg));
        buffer.DrawBox(modalRect, BorderFg, ModalBg);

        buffer.WriteString(mx + 2, my + 1, "Extensions", TitleFg, ModalBg);
        buffer.WriteString(mx + 2, my + 2, "Filter: ", HintFg, ModalBg);
        buffer.WriteString(mx + 10, my + 2, _filter.ToString(), FilterFg, ModalBg);

        for (var x = 0; x < ModalWidth - 2; x++)
            buffer.Set(mx + 1 + x, my + 3, new Cell('─', HintFg, ModalBg));

        var listStartY = my + ListStartRow;
        var visibleCount = Math.Min(listHeight, Math.Max(0, _filtered.Count - _scrollOffset));
        for (var i = 0; i < visibleCount; i++)
        {
            var extensionIndex = _scrollOffset + i;
            var extension = _filtered[extensionIndex];
            var isSelected = extensionIndex == _selectedIndex;
            var fg = isSelected ? SelectedFg : ItemFg;
            var bg = isSelected ? SelectedBg : ModalBg;
            var rowWidth = ModalWidth - 4;

            if (isSelected)
                buffer.Fill(new Rect(mx + 2, listStartY + i, rowWidth, 1), new Cell(' ', fg, bg));

            var rowText = FormatRow(extension, rowWidth);
            buffer.WriteString(mx + 2, listStartY + i, rowText, fg, bg);
        }

        if (_scrollOffset > 0)
            buffer.WriteString(mx + ModalWidth - 4, listStartY, "▲", HintFg, ModalBg);
        if (_scrollOffset + listHeight < _filtered.Count)
            buffer.WriteString(mx + ModalWidth - 4, listStartY + listHeight - 1, "▼", HintFg, ModalBg);

        var detailTop = my + ModalHeight - DetailHeight - 1;
        for (var x = 0; x < ModalWidth - 2; x++)
            buffer.Set(mx + 1 + x, detailTop - 1, new Cell('─', HintFg, ModalBg));

        if (SelectedExtension is { } selected)
        {
            buffer.WriteString(mx + 2, detailTop, selected.Name, TitleFg, ModalBg);
            buffer.WriteString(mx + 2, detailTop + 1, selected.Description, HintFg, ModalBg);
            buffer.WriteString(
                mx + 2,
                detailTop + 2,
                $"Version {selected.Version}  Tier {selected.Tier}  Default {(selected.DefaultEnabled ? "enabled" : "disabled")}",
                HintFg,
                ModalBg);
        }
        else
        {
            buffer.WriteString(mx + 2, detailTop, "No extensions match the current filter.", HintFg, ModalBg);
        }

        var footer = BuildFooterText();
        var footerColor = FeedbackMessage is null
            ? HintFg
            : FeedbackIsError ? FooterErrorFg : FooterOkFg;
        buffer.WriteString(mx + 2, my + ModalHeight - 2, footer, footerColor, ModalBg);
    }

    public bool HandleKey(KeyEvent key)
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

            case Key.Up:
                CancelConfirmation();
                MoveSelection(-1);
                return true;

            case Key.Down:
                CancelConfirmation();
                MoveSelection(1);
                return true;

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

        _selectedIndex = 0;
        if (preferredSelectionId is not null)
        {
            var index = _filtered.FindIndex(extension => extension.Id == preferredSelectionId);
            if (index >= 0)
                _selectedIndex = index;
        }

        _scrollOffset = 0;
        EnsureVisible();
    }

    private void MoveSelection(int delta)
    {
        if (_filtered.Count == 0)
            return;

        _selectedIndex = Math.Clamp(_selectedIndex + delta, 0, _filtered.Count - 1);
        EnsureVisible();
    }

    private void EnsureVisible()
    {
        var listHeight = ModalHeight - ListStartRow - DetailHeight - 2;
        if (_selectedIndex < _scrollOffset)
            _scrollOffset = _selectedIndex;
        else if (_selectedIndex >= _scrollOffset + listHeight)
            _scrollOffset = _selectedIndex - listHeight + 1;
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
