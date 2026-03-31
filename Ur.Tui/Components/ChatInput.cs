using System.Text;
using Ur.Terminal.Components;
using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Components;

public sealed class ChatInput : IComponent
{
    private const int MaxVisibleLines = 5;
    private static readonly Color TextFg = Color.White;
    private static readonly Color Bg = Color.Black;
    private static readonly Color CursorFg = Color.Black;
    private static readonly Color CursorBg = Color.White;
    private static readonly Color BorderFg = new(80, 80, 80);

    private readonly List<StringBuilder> _lines = [new()];
    private int _cursorLine;
    private int _cursorCol;
    private int _scrollOffset;
    private int _width;

    /// <summary>A visual row mapped back to its logical line and column range.</summary>
    private readonly record struct VisualRow(int LogicalLine, int StartCol, int Length, string Text);

    public string Text
    {
        get
        {
            var sb = new StringBuilder();
            for (var i = 0; i < _lines.Count; i++)
            {
                if (i > 0) sb.Append('\n');
                sb.Append(_lines[i]);
            }
            return sb.ToString();
        }
    }

    /// <summary>Top border + visible text lines + bottom border.</summary>
    public int GetInputHeight(int width)
    {
        var totalVisualLines = CountVisualLines(GetContentWidth(width));
        return 2 + Math.Min(totalVisualLines, MaxVisibleLines);
    }

    public void Clear()
    {
        _lines.Clear();
        _lines.Add(new StringBuilder());
        _cursorLine = 0;
        _cursorCol = 0;
        _scrollOffset = 0;
    }

    public void Render(Buffer buffer, Rect area)
    {
        if (area.Width < 1 || area.Height < 1)
            return;

        _width = GetContentWidth(area.Width);

        buffer.Fill(area, new Cell(' ', TextFg, Bg));
        RenderBorder(buffer, area);

        var bottomY = area.Bottom - 1;
        var contentX = area.X + 1;
        var visualRows = BuildVisualRows(_width);
        var totalVisual = visualRows.Count;
        var (cursorVisualRow, cursorVisualCol) = FindCursorVisual(visualRows);

        // Adjust scroll offset to keep cursor visible
        if (cursorVisualRow < _scrollOffset)
            _scrollOffset = cursorVisualRow;
        else if (cursorVisualRow >= _scrollOffset + MaxVisibleLines)
            _scrollOffset = cursorVisualRow - MaxVisibleLines + 1;

        // Clamp scroll offset
        var maxScroll = Math.Max(0, totalVisual - MaxVisibleLines);
        _scrollOffset = Math.Clamp(_scrollOffset, 0, maxScroll);

        var visibleCount = Math.Min(totalVisual - _scrollOffset, MaxVisibleLines);
        for (var i = 0; i < visibleCount; i++)
        {
            var rowIdx = _scrollOffset + i;
            if (rowIdx >= totalVisual) break;

            var row = visualRows[rowIdx];
            var y = area.Y + 1 + i;
            if (y >= bottomY) break;

            buffer.WriteString(contentX, y, row.Text, TextFg, Bg);

            if (rowIdx == cursorVisualRow)
            {
                var cursorX = contentX + cursorVisualCol;
                if (cursorX < area.Right - 1)
                {
                    if (cursorVisualCol < row.Text.Length)
                    {
                        buffer.Set(cursorX, y, new Cell(row.Text[cursorVisualCol], CursorFg, CursorBg));
                    }
                    else
                    {
                        buffer.Set(cursorX, y, new Cell(' ', CursorFg, CursorBg));
                    }
                }
            }
        }
    }

    public bool HandleKey(KeyEvent key)
    {
        switch (key.Key)
        {
            case Key.Enter when key.Mods.HasFlag(Modifiers.Shift):
                InsertNewline();
                return true;

            case Key.Enter:
                return false; // Signal to app: input submitted

            case Key.A when key.Mods.HasFlag(Modifiers.Ctrl):
                _cursorCol = 0;
                return true;

            case Key.E when key.Mods.HasFlag(Modifiers.Ctrl):
                _cursorCol = _lines[_cursorLine].Length;
                return true;

            case Key.Backspace:
                if (_cursorCol > 0)
                {
                    _lines[_cursorLine].Remove(_cursorCol - 1, 1);
                    _cursorCol--;
                }
                else if (_cursorLine > 0)
                {
                    var prevLine = _lines[_cursorLine - 1];
                    var newCol = prevLine.Length;
                    prevLine.Append(_lines[_cursorLine]);
                    _lines.RemoveAt(_cursorLine);
                    _cursorLine--;
                    _cursorCol = newCol;
                }
                return true;

            case Key.Delete:
                if (_cursorCol < _lines[_cursorLine].Length)
                {
                    _lines[_cursorLine].Remove(_cursorCol, 1);
                }
                else if (_cursorLine < _lines.Count - 1)
                {
                    _lines[_cursorLine].Append(_lines[_cursorLine + 1]);
                    _lines.RemoveAt(_cursorLine + 1);
                }
                return true;

            case Key.Left:
                if (_cursorCol > 0)
                    _cursorCol--;
                else if (_cursorLine > 0)
                {
                    _cursorLine--;
                    _cursorCol = _lines[_cursorLine].Length;
                }
                return true;

            case Key.Right:
                if (_cursorCol < _lines[_cursorLine].Length)
                    _cursorCol++;
                else if (_cursorLine < _lines.Count - 1)
                {
                    _cursorLine++;
                    _cursorCol = 0;
                }
                return true;

            case Key.Up:
                MoveCursorVertical(-1);
                return true;

            case Key.Down:
                MoveCursorVertical(1);
                return true;

            case Key.Home:
                _cursorCol = 0;
                return true;

            case Key.End:
                _cursorCol = _lines[_cursorLine].Length;
                return true;

            default:
                if (key.Char.HasValue)
                {
                    _lines[_cursorLine].Insert(_cursorCol, key.Char.Value);
                    _cursorCol++;
                    return true;
                }
                return true;
        }
    }

    private void InsertNewline()
    {
        var currentLine = _lines[_cursorLine];
        var remainder = currentLine.ToString(_cursorCol, currentLine.Length - _cursorCol);
        currentLine.Remove(_cursorCol, currentLine.Length - _cursorCol);
        _cursorLine++;
        _lines.Insert(_cursorLine, new StringBuilder(remainder));
        _cursorCol = 0;
    }

    private void MoveCursorVertical(int direction)
    {
        if (_width <= 0) return;

        var visualRows = BuildVisualRows(_width);
        var (curRow, curCol) = FindCursorVisual(visualRows);

        var targetRow = curRow + direction;
        if (targetRow < 0 || targetRow >= visualRows.Count) return;

        var target = visualRows[targetRow];
        _cursorLine = target.LogicalLine;
        _cursorCol = target.StartCol + Math.Min(curCol, target.Length);
        _cursorCol = Math.Min(_cursorCol, _lines[_cursorLine].Length);
    }

    private int CountVisualLines(int width)
    {
        if (width <= 0) return _lines.Count;

        var count = 0;
        for (var i = 0; i < _lines.Count; i++)
        {
            var text = _lines[i].ToString();
            count += text.Length == 0 ? 1 : WrapSegments(text, width).Count;
        }
        return count;
    }

    private List<VisualRow> BuildVisualRows(int width)
    {
        var rows = new List<VisualRow>();
        for (var i = 0; i < _lines.Count; i++)
        {
            var text = _lines[i].ToString();
            if (text.Length == 0)
            {
                rows.Add(new VisualRow(i, 0, 0, ""));
                continue;
            }

            foreach (var (startCol, length) in WrapSegments(text, width))
                rows.Add(new VisualRow(i, startCol, length, text.Substring(startCol, length)));
        }
        return rows;
    }

    private (int Row, int Col) FindCursorVisual(List<VisualRow> visualRows)
    {
        for (var i = 0; i < visualRows.Count; i++)
        {
            var row = visualRows[i];
            if (row.LogicalLine != _cursorLine) continue;

            if (_cursorCol >= row.StartCol && _cursorCol < row.StartCol + row.Length)
                return (i, _cursorCol - row.StartCol);

            // Cursor at or past the end of the last segment for this logical line
            var isLastSegment = i + 1 >= visualRows.Count || visualRows[i + 1].LogicalLine != _cursorLine;
            if (isLastSegment && _cursorCol >= row.StartCol)
                return (i, _cursorCol - row.StartCol);
        }

        return (visualRows.Count - 1, 0);
    }

    /// <summary>
    /// Splits a logical line into wrap segments at word boundaries when possible,
    /// hard-breaking when no space is found. Every character is accounted for in
    /// exactly one segment (spaces at break points stay at the end of their segment).
    /// </summary>
    private static List<(int StartCol, int Length)> WrapSegments(string text, int width)
    {
        var segments = new List<(int, int)>();
        if (width <= 0 || text.Length == 0)
        {
            segments.Add((0, text.Length));
            return segments;
        }

        var pos = 0;
        while (pos < text.Length)
        {
            var remaining = text.Length - pos;
            if (remaining <= width)
            {
                segments.Add((pos, remaining));
                break;
            }

            // Search backwards for a space to break at
            var lastSpace = -1;
            for (var i = pos + width - 1; i > pos; i--)
            {
                if (text[i] == ' ')
                {
                    lastSpace = i;
                    break;
                }
            }

            if (lastSpace > pos)
            {
                // Include the space in the current segment, continue after it
                var len = lastSpace - pos + 1;
                segments.Add((pos, len));
                pos = lastSpace + 1;
            }
            else
            {
                // No word boundary found; hard break at width
                segments.Add((pos, width));
                pos += width;
            }
        }

        return segments;
    }

    private static int GetContentWidth(int totalWidth)
    {
        return Math.Max(0, totalWidth - 2);
    }

    private static void RenderBorder(Buffer buffer, Rect area)
    {
        var rightX = area.Right - 1;
        var bottomY = area.Bottom - 1;

        buffer.Set(area.X, area.Y, new Cell('┌', BorderFg, Bg));
        if (rightX > area.X)
            buffer.Set(rightX, area.Y, new Cell('┐', BorderFg, Bg));

        if (bottomY > area.Y)
        {
            buffer.Set(area.X, bottomY, new Cell('└', BorderFg, Bg));
            if (rightX > area.X)
                buffer.Set(rightX, bottomY, new Cell('┘', BorderFg, Bg));
        }

        for (var x = area.X + 1; x < rightX; x++)
        {
            buffer.Set(x, area.Y, new Cell('─', BorderFg, Bg));
            if (bottomY > area.Y)
                buffer.Set(x, bottomY, new Cell('─', BorderFg, Bg));
        }

        for (var y = area.Y + 1; y < bottomY; y++)
        {
            buffer.Set(area.X, y, new Cell('│', BorderFg, Bg));
            if (rightX > area.X)
                buffer.Set(rightX, y, new Cell('│', BorderFg, Bg));
        }
    }
}
