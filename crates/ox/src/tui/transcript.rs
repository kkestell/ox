use std::collections::HashMap;
use std::time::Instant;

use agent_client_protocol::schema::v1::*;
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span};
use serde_json::Value;
use similar::{ChangeTag, TextDiff};
use unicode_width::{UnicodeWidthChar, UnicodeWidthStr};

use super::escape;
use super::theme;

/// Seconds before the thinking placeholder starts counting.
const COUNT_AFTER: u64 = 10;

/// The content rows a truncated call shows.
const TRUNCATED_ROWS: usize = 5;

/// How much of a named call's content the transcript shows.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum ToolOutput {
    #[default]
    Summary,
    Truncated,
    Full,
}

impl ToolOutput {
    pub fn next(self) -> Self {
        match self {
            ToolOutput::Summary => ToolOutput::Truncated,
            ToolOutput::Truncated => ToolOutput::Full,
            ToolOutput::Full => ToolOutput::Summary,
        }
    }
}

pub enum Item {
    User(String),
    Thinking {
        text: String,
        started: Instant,
        ended: Option<Instant>,
    },
    Response(String),
    Tool(Box<ToolCall>),
    Notice {
        text: String,
        color: Color,
    },
}

#[derive(Default)]
pub struct TranscriptView {
    items: Vec<Item>,
    tools: HashMap<ToolCallId, usize>,
    /// Whether the last update was a user message chunk.
    user_chunk_open: bool,
    /// The manual top row while auto-scroll is off.
    top: Option<usize>,
    pub new_activity: bool,
}

impl TranscriptView {
    pub fn user(&mut self, text: String, now: Instant) {
        self.user_chunk_open = false;
        self.end_thinking(now);
        self.tools.clear();
        self.items.push(Item::User(text));
        self.changed();
    }

    pub fn notice(&mut self, text: String, color: Color, now: Instant) {
        self.user_chunk_open = false;
        self.end_thinking(now);
        self.items.push(Item::Notice { text, color });
        self.changed();
    }

    pub fn end_turn(&mut self, now: Instant) {
        self.user_chunk_open = false;
        self.end_thinking(now);
        self.tools.clear();
    }

    pub fn update(&mut self, update: SessionUpdate, now: Instant) {
        let continue_user = self.user_chunk_open;
        self.user_chunk_open = matches!(&update, SessionUpdate::UserMessageChunk(_));
        match update {
            SessionUpdate::UserMessageChunk(chunk) => {
                self.end_thinking(now);
                let text = content(&chunk.content);
                match self.items.last_mut() {
                    Some(Item::User(open)) if continue_user => open.push_str(&text),
                    _ => {
                        self.tools.clear();
                        self.items.push(Item::User(text));
                    }
                }
            }
            SessionUpdate::AgentThoughtChunk(chunk) => {
                let text = content(&chunk.content);
                match self.items.last_mut() {
                    Some(Item::Thinking {
                        text: open,
                        ended: None,
                        ..
                    }) => open.push_str(&text),
                    _ => self.items.push(Item::Thinking {
                        text,
                        started: now,
                        ended: None,
                    }),
                }
            }
            SessionUpdate::AgentMessageChunk(chunk) => {
                self.end_thinking(now);
                let text = content(&chunk.content);
                match self.items.last_mut() {
                    Some(Item::Response(open)) => open.push_str(&text),
                    _ => self.items.push(Item::Response(text)),
                }
            }
            SessionUpdate::ToolCall(call) => {
                self.end_thinking(now);
                match self.tools.get(&call.tool_call_id) {
                    Some(&index) => self.items[index] = Item::Tool(Box::new(call)),
                    None => {
                        self.tools
                            .insert(call.tool_call_id.clone(), self.items.len());
                        self.items.push(Item::Tool(Box::new(call)));
                    }
                }
            }
            SessionUpdate::ToolCallUpdate(update) => {
                self.end_thinking(now);
                match self.tools.get(&update.tool_call_id) {
                    Some(&index) => match &mut self.items[index] {
                        Item::Tool(call) => call.update(update.fields),
                        _ => unreachable!("tool indexes point at tool items"),
                    },
                    None => {
                        let mut call = ToolCall::new(update.tool_call_id.clone(), "");
                        call.update(update.fields);
                        self.tools.insert(update.tool_call_id, self.items.len());
                        self.items.push(Item::Tool(Box::new(call)));
                    }
                }
            }
            _ => {
                self.end_thinking(now);
                return;
            }
        }
        self.changed();
    }

    pub fn tool(&self, id: &ToolCallId) -> Option<&ToolCall> {
        match self.tools.get(id).map(|&index| &self.items[index]) {
            Some(Item::Tool(call)) => Some(call.as_ref()),
            _ => None,
        }
    }

    fn end_thinking(&mut self, now: Instant) {
        if let Some(Item::Thinking { ended, .. }) = self.items.last_mut()
            && ended.is_none()
        {
            *ended = Some(now);
        }
    }

    /// Notes a change that arrived while the view is scrolled up.
    pub fn changed(&mut self) {
        if self.top.is_some() {
            self.new_activity = true;
        }
    }

    pub fn page_up(&mut self, height: usize, total: usize) {
        if total <= height {
            return;
        }
        let top = self.top.unwrap_or(total - height);
        self.top = Some(top.saturating_sub(page(height)));
    }

    pub fn page_down(&mut self, height: usize, total: usize) {
        if let Some(top) = self.top {
            let next = top + page(height);
            if next >= total.saturating_sub(height) {
                self.end();
            } else {
                self.top = Some(next);
            }
        }
    }

    pub fn end(&mut self) {
        self.top = None;
        self.new_activity = false;
    }

    /// The first row shown of `total` rows in a view `height` rows tall.
    pub fn first_row(&self, height: usize, total: usize) -> usize {
        let bottom = total.saturating_sub(height);
        match self.top {
            Some(top) => top.min(bottom),
            None => bottom,
        }
    }

    /// The rows of every item, one blank row between items.
    pub fn lines(
        &self,
        width: usize,
        show_thinking: bool,
        output: ToolOutput,
        now: Instant,
    ) -> Vec<Line<'static>> {
        let mut lines = Vec::new();
        for item in &self.items {
            let item_lines = item_lines(item, width, show_thinking, output, now);
            if item_lines.is_empty() {
                continue;
            }
            if !lines.is_empty() {
                lines.push(Line::default());
            }
            lines.extend(item_lines);
        }
        lines
    }
}

fn page(height: usize) -> usize {
    height.saturating_sub(1).max(1)
}

fn gray() -> Style {
    Style::new().fg(theme::DIM)
}

/// An item's rows. A named call shows as much of its content as `output`
/// allows; a nameless call, such as a subagent's answer, always shows all of
/// it.
fn item_lines(
    item: &Item,
    width: usize,
    show_thinking: bool,
    output: ToolOutput,
    now: Instant,
) -> Vec<Line<'static>> {
    match item {
        Item::User(text) => prefixed(markdown(text, Style::new()), width, "❯ ", "  "),
        Item::Thinking {
            text,
            started,
            ended,
        } => {
            if show_thinking && !text.trim().is_empty() {
                prefixed(markdown(text, gray()), width, "● ", "  ")
            } else {
                let placeholder = placeholder(*started, *ended, now);
                vec![Line::styled(format!("● {placeholder}"), gray())]
            }
        }
        Item::Response(text) => prefixed(markdown(text, Style::new()), width, "● ", "  "),
        Item::Tool(call) => {
            let mut lines = vec![
                shell_line(call, width).unwrap_or_else(|| tool_line(call, &call.title, width)),
            ];
            lines.extend(match output {
                _ if call.name.is_none() => content_lines(call, width, "  └ ", "    ", gray()),
                ToolOutput::Summary => Vec::new(),
                ToolOutput::Truncated => {
                    let mut rows = content_rows(call, width.saturating_sub(4), gray());
                    // Hiding a single row would not save a row.
                    if rows.len() > TRUNCATED_ROWS + 1 {
                        let hidden = rows.len() - TRUNCATED_ROWS;
                        rows.drain(..hidden);
                        let count = format!("...{hidden} more lines");
                        rows.insert(0, Line::styled(count, Style::new().fg(theme::FAINT)));
                    }
                    prefixed(rows, width, "  └ ", "    ")
                }
                ToolOutput::Full => content_lines(call, width, "  └ ", "    ", gray()),
            });
            lines
        }
        Item::Notice { text, color } => wrap(plain(text, Style::new().fg(*color)), width),
    }
}

fn placeholder(started: Instant, ended: Option<Instant>, now: Instant) -> String {
    match ended {
        Some(ended) => format!(
            "Thought for {}s",
            ended.saturating_duration_since(started).as_secs()
        ),
        None => {
            let secs = now.saturating_duration_since(started).as_secs();
            if secs < COUNT_AFTER {
                "Thinking...".to_owned()
            } else {
                format!("Thinking for {secs}s...")
            }
        }
    }
}

/// A call's `●` row and its content, indented by two columns.
pub fn described_lines(call: &ToolCall, width: usize, style: Style) -> Vec<Line<'static>> {
    let mut lines = vec![tool_line(call, &call.title, width)];
    lines.extend(content_lines(call, width, "  ", "  ", style));
    lines
}

/// A call's content rows with `first` before the first row and `rest`, of the
/// same width, before the others.
pub fn content_lines(
    call: &ToolCall,
    width: usize,
    first: &str,
    rest: &str,
    style: Style,
) -> Vec<Line<'static>> {
    let rows = content_rows(call, width.saturating_sub(first.width()), style);
    prefixed(rows, width, first, rest)
}

/// A call's content rows `width` columns wide. Text blocks wrap, as Markdown
/// when the call is nameless, such as a subagent's answer; diff blocks are
/// unified hunks, clipped so their indentation survives.
fn content_rows(call: &ToolCall, width: usize, style: Style) -> Vec<Line<'static>> {
    let mut lines = Vec::new();
    for block in &call.content {
        match block {
            ToolCallContent::Content(item) => {
                let text = content(&item.content);
                lines.extend(if call.name.is_none() {
                    markdown(&text, style)
                } else {
                    plain(&text, style)
                });
            }
            ToolCallContent::Diff(diff) => lines.extend(
                diff_rows(diff, width, style)
                    .into_iter()
                    .map(|(row, style)| Line::styled(row, style)),
            ),
            _ => lines.push(Line::styled("[non-text content]", style)),
        }
    }
    wrap(lines, width)
}

/// The unified hunks of a diff with three rows of context. Hunk headers are
/// dim, removed rows red, and added rows green; context rows keep `style`.
fn diff_rows(diff: &Diff, width: usize, style: Style) -> Vec<(String, Style)> {
    let old = diff.old_text.as_deref().unwrap_or("");
    let text_diff = TextDiff::from_lines(old, &diff.new_text);
    let mut rows = Vec::new();
    for hunk in text_diff.unified_diff().context_radius(3).iter_hunks() {
        rows.push((
            clip(&hunk.header().to_string(), width),
            Style::new().fg(theme::DIM),
        ));
        for change in hunk.iter_changes() {
            let (sign, style) = match change.tag() {
                ChangeTag::Delete => ('-', Style::new().fg(theme::RED)),
                ChangeTag::Insert => ('+', Style::new().fg(theme::GREEN)),
                ChangeTag::Equal => (' ', style),
            };
            let line = expand(change.value().trim_end_matches(['\n', '\r']));
            rows.push((clip(&format!("{sign}{line}"), width), style));
        }
    }
    rows
}

fn icon(call: &ToolCall, symbol: &str) -> Span<'static> {
    let style = match call.status {
        ToolCallStatus::Pending => Style::new().fg(theme::YELLOW),
        ToolCallStatus::Completed => Style::new().fg(theme::GREEN),
        ToolCallStatus::Failed => Style::new().fg(theme::RED),
        _ => Style::new(),
    };
    Span::styled(symbol.to_owned(), style)
}

fn tool_line(call: &ToolCall, title: &str, width: usize) -> Line<'static> {
    Line::from(vec![
        icon(call, "●"),
        Span::raw(" "),
        Span::raw(clip(title, width.saturating_sub(2))),
    ])
}

/// The one-row `● Shell` line of a shell call, or nothing when the call is not
/// a shell command.
fn shell_line(call: &ToolCall, width: usize) -> Option<Line<'static>> {
    if call.name.as_deref() != Some("shell") {
        return None;
    }
    let input = call.raw_input.as_ref()?;
    let command = input.get("command")?.as_str()?;
    let background = input
        .get("background")
        .and_then(Value::as_bool)
        .unwrap_or(false);
    let mut title = format!(
        "Shell {}",
        command.split_whitespace().collect::<Vec<_>>().join(" ")
    );
    if background {
        title.push_str(" &");
    }
    Some(tool_line(call, &title, width))
}

/// Wrapped rows with `first` before the first and `rest`, of the same width,
/// before the others.
fn prefixed(
    lines: Vec<Line<'static>>,
    width: usize,
    first: &str,
    rest: &str,
) -> Vec<Line<'static>> {
    wrap(lines, width.saturating_sub(first.width()))
        .into_iter()
        .enumerate()
        .map(|(index, mut row)| {
            let prefix = if index == 0 { first } else { rest };
            if index == 0 || row.width() > 0 {
                row.spans.insert(0, Span::raw(prefix.to_owned()));
            }
            row
        })
        .collect()
}

#[derive(Clone)]
struct Styles;

impl tui_markdown::StyleSheet for Styles {
    fn heading(&self, _level: u8) -> Style {
        Style::new().add_modifier(Modifier::BOLD)
    }

    fn heading_marker(&self, _level: u8) -> &str {
        ""
    }

    fn code(&self) -> Style {
        Style::new().fg(theme::LIGHT_YELLOW)
    }

    fn code_block_fence(&self) -> &str {
        ""
    }

    fn link(&self) -> Style {
        Style::new().add_modifier(Modifier::UNDERLINED)
    }

    fn blockquote(&self) -> Style {
        Style::new().fg(theme::GRAY)
    }

    fn list_marker(&self) -> Style {
        Style::new()
    }

    fn table_header(&self) -> Style {
        Style::new().add_modifier(Modifier::BOLD)
    }

    fn table_border(&self) -> Style {
        Style::new().fg(theme::DIM)
    }
}

/// Escaped Markdown as unwrapped rows of styled spans. `style` wins over the
/// Markdown styles, so dim text stays dim. Every line break is kept: two
/// trailing spaces make a Markdown line break, so typed and streamed breaks
/// show as typed instead of joining into one paragraph.
fn markdown(text: &str, style: Style) -> Vec<Line<'static>> {
    let text = escape(text).replace('\n', "  \n");
    let options = tui_markdown::Options::new(Styles);
    tui_markdown::from_str_with_options(&text, &options)
        .lines
        .into_iter()
        .map(|line| {
            let spans = line
                .spans
                .into_iter()
                .map(|span| {
                    let span_style = line.style.patch(span.style).patch(style);
                    Span::styled(span.content.into_owned(), span_style)
                })
                .collect::<Vec<_>>();
            Line::from(spans).style(style)
        })
        .collect()
}

/// Escaped and trimmed text as unwrapped rows, one per line.
fn plain(text: &str, style: Style) -> Vec<Line<'static>> {
    escape(text)
        .trim()
        .split('\n')
        .map(|line| Line::styled(line.to_owned(), style))
        .collect()
}

/// A row's hanging prefix: the `>` of a quote and blank space as wide as a list
/// marker, which the rows after the first of a wrapped row start with.
fn hanging(line: &Line<'static>) -> Vec<Span<'static>> {
    let mut hang = Vec::new();
    let mut spans = line.spans.iter().peekable();
    while let Some(span) = spans.next_if(|span| span.content == ">" || span.content == " ") {
        hang.push(span.clone());
    }
    if let Some(span) = spans.next() {
        let marker = span.content.trim_start();
        let list = marker == "- "
            || marker.starts_with("- [")
            || marker
                .strip_suffix(". ")
                .is_some_and(|n| !n.is_empty() && n.bytes().all(|b| b.is_ascii_digit()));
        if list {
            hang.push(Span::styled(" ".repeat(span.width()), span.style));
        }
    }
    hang
}

/// Word-wraps rows by display width. The whole text is trimmed, interior
/// blank rows are kept, tabs are expanded, and words wider than a row are
/// split. The rows after the first of a wrapped row start with its hanging
/// prefix.
pub fn wrap(lines: Vec<Line<'static>>, width: usize) -> Vec<Line<'static>> {
    let width = width.max(1);
    let mut rows: Vec<Line<'static>> = lines
        .iter()
        .flat_map(|line| wrap_line(line, width))
        .collect();
    while rows.last().is_some_and(|row| row.width() == 0) {
        rows.pop();
    }
    let blank = rows.iter().take_while(|row| row.width() == 0).count();
    rows.drain(..blank);
    rows
}

fn wrap_line(line: &Line<'static>, width: usize) -> Vec<Line<'static>> {
    let hang = hanging(line);
    let hang_width = hang.iter().map(Span::width).sum::<usize>().min(width - 1);
    // Every character with its style, tabs expanded, trailing blanks dropped.
    let mut chars: Vec<(char, Style)> = Vec::new();
    let mut column = 0;
    for span in &line.spans {
        let style = line.style.patch(span.style);
        for c in span.content.chars() {
            if c == '\t' {
                let spaces = 8 - column % 8;
                chars.extend(std::iter::repeat_n((' ', style), spaces));
                column += spaces;
            } else {
                chars.push((c, style));
                column += c.width().unwrap_or(0);
            }
        }
    }
    while chars.last().is_some_and(|(c, _)| c.is_whitespace()) {
        chars.pop();
    }
    let text_width = |chars: &[(char, Style)]| -> usize {
        chars.iter().map(|(c, _)| c.width().unwrap_or(0)).sum()
    };
    let mut rows: Vec<Vec<(char, Style)>> = vec![Vec::new()];
    let mut column = 0;
    let mut rest = &chars[..];
    while !rest.is_empty() {
        let row_width = if rows.len() == 1 {
            width
        } else {
            width - hang_width
        };
        let space_end = rest
            .iter()
            .position(|(c, _)| !c.is_whitespace())
            .unwrap_or(rest.len());
        let word_end = rest[space_end..]
            .iter()
            .position(|(c, _)| c.is_whitespace())
            .map_or(rest.len(), |end| space_end + end);
        let (mut space, word) = (&rest[..space_end], &rest[space_end..word_end]);
        if column > 0 && column + text_width(space) + text_width(word) > row_width {
            rows.push(Vec::new());
            column = 0;
            space = &[];
        }
        for &(c, style) in space.iter().chain(word) {
            let row_width = if rows.len() == 1 {
                width
            } else {
                width - hang_width
            };
            let c_width = c.width().unwrap_or(0);
            if column + c_width > row_width && column > 0 {
                rows.push(Vec::new());
                column = 0;
            }
            rows.last_mut().unwrap().push((c, style));
            column += c_width;
        }
        rest = &rest[word_end..];
    }
    rows.into_iter()
        .enumerate()
        .map(|(index, row)| {
            let mut spans: Vec<Span<'static>> = Vec::new();
            for (c, style) in row {
                match spans.last_mut() {
                    Some(last) if last.style == style => last.content.to_mut().push(c),
                    _ => spans.push(Span::styled(c.to_string(), style)),
                }
            }
            if index > 0 {
                spans.splice(0..0, hang.iter().cloned());
            }
            Line::from(spans).style(line.style)
        })
        .collect()
}

fn content(block: &ContentBlock) -> String {
    match block {
        ContentBlock::Text(text) => text.text.clone(),
        _ => "[non-text content]".to_owned(),
    }
}

/// Replaces tabs with spaces up to the next tab stop.
fn expand(line: &str) -> String {
    let mut text = String::new();
    let mut column = 0;
    for c in line.chars() {
        if c == '\t' {
            let spaces = 8 - column % 8;
            text.push_str(&" ".repeat(spaces));
            column += spaces;
        } else {
            text.push(c);
            column += c.width().unwrap_or(0);
        }
    }
    text
}

/// Escapes control characters and clips the text with `…`.
pub fn clip(text: &str, width: usize) -> String {
    let text = escape(text);
    if text.width() <= width {
        return text;
    }
    let mut clipped = String::new();
    let mut used = 0;
    for c in text.chars() {
        let c_width = c.width().unwrap_or(0);
        if used + c_width + 1 > width {
            break;
        }
        clipped.push(c);
        used += c_width;
    }
    if width > 0 {
        clipped.push('…');
    }
    clipped
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use super::*;

    /// The rows with tool output hidden.
    fn rows(view: &TranscriptView, width: usize, show: bool, now: Instant) -> Vec<String> {
        text(&view.lines(width, show, ToolOutput::Summary, now))
    }

    fn text(lines: &[Line]) -> Vec<String> {
        lines.iter().map(ToString::to_string).collect()
    }

    /// The color of a line, whether set on the line or on its first span.
    fn color(line: &Line) -> Option<Color> {
        line.style
            .fg
            .or_else(|| line.spans.first().and_then(|span| span.style.fg))
    }

    fn thought(text: &str) -> SessionUpdate {
        SessionUpdate::AgentThoughtChunk(ContentChunk::new(text.into()))
    }

    fn message(text: &str) -> SessionUpdate {
        SessionUpdate::AgentMessageChunk(ContentChunk::new(text.into()))
    }

    fn read(id: &str, path: &str) -> SessionUpdate {
        SessionUpdate::ToolCall(
            ToolCall::new(id.to_owned(), format!("Read {path}"))
                .name("read_file".to_owned())
                .status(ToolCallStatus::Completed),
        )
    }

    fn shell(id: &str, command: &str, background: bool, status: ToolCallStatus) -> SessionUpdate {
        SessionUpdate::ToolCall(
            ToolCall::new(id.to_owned(), "shell")
                .name("shell".to_owned())
                .status(status)
                .raw_input(serde_json::json!({"command": command, "background": background})),
        )
    }

    fn answer(id: &str, text: &str) -> SessionUpdate {
        SessionUpdate::ToolCall(
            ToolCall::new(id.to_owned(), "Final answer from subagent child-1")
                .status(ToolCallStatus::Completed)
                .content(vec![ToolCallContent::from(ContentBlock::from(text))]),
        )
    }

    fn view(updates: Vec<SessionUpdate>, now: Instant) -> TranscriptView {
        let mut view = TranscriptView::default();
        for update in updates {
            view.update(update, now);
        }
        view
    }

    #[test]
    fn text_is_trimmed_and_wrapped_by_display_width() {
        let now = Instant::now();
        for (case, text, width, expected) in [
            (
                "surrounding whitespace",
                "  \n\t hello world \n\n ",
                40,
                vec!["hello world"],
            ),
            ("whitespace only", " \n\t   ", 40, vec![]),
            (
                "interior blank lines",
                "one\n\n\ntwo\n",
                40,
                vec!["one", "", "", "two"],
            ),
            ("explicit newlines", "a\nb  \n c", 40, vec!["a", "b", " c"]),
            ("tabs", "a\tb\t\tc", 40, vec!["a       b               c"]),
            (
                "word wrap",
                "one two three four",
                10,
                vec!["one two", "three four"],
            ),
            ("long word", "ab abcdefgh", 6, vec!["ab", "abcdef", "gh"]),
            (
                "display width",
                "界界 界界界 界界界界",
                7,
                vec!["界界", "界界界", "界界界", "界"],
            ),
            (
                "escaped control characters",
                "a\rb \x1b",
                20,
                vec!["a\\rb \\u{1b}"],
            ),
        ] {
            assert_eq!(
                self::text(&wrap(plain(text, Style::new()), width)),
                expected,
                "{case}"
            );
        }
        let text = "  first line of text\nsecond  ";
        let mut user = TranscriptView::default();
        user.user(text.to_owned(), now);
        assert_eq!(
            rows(&user, 12, false, now),
            ["❯ first line", "  of text", "  second"]
        );
        let response = view(vec![message(text)], now);
        assert_eq!(
            rows(&response, 12, false, now),
            ["● first line", "  of text", "  second"]
        );
    }

    #[test]
    fn messages_render_as_markdown_with_hanging_list_and_quote_rows() {
        let now = Instant::now();
        for (case, text, width, expected) in [
            (
                "line breaks are kept",
                "one\ntwo\n\nthree",
                40,
                vec!["● one", "  two", "", "  three"],
            ),
            (
                "headings drop their marker",
                "# Title\n\nbody",
                40,
                vec!["● Title", "", "  body"],
            ),
            (
                "wrapped list items hang under the marker",
                "- one two three four\n  - five six seven\n\n1. eight nine ten",
                18,
                vec![
                    "● - one two three",
                    "    four",
                    "      - five six",
                    "        seven",
                    "",
                    "  1. eight nine",
                    "     ten",
                ],
            ),
            (
                "wrapped quotes keep their bar",
                "> one two three four",
                14,
                vec!["● > one two", "  > three four"],
            ),
            (
                "code blocks keep their rows without fences",
                "```\nfn a() {\n    b()\n}\n```",
                40,
                vec!["● fn a() {", "      b()", "  }"],
            ),
        ] {
            assert_eq!(
                rows(&view(vec![message(text)], now), width, false, now),
                expected,
                "{case}"
            );
        }
        let lines =
            view(vec![message("**bold** `code`")], now).lines(40, false, ToolOutput::Summary, now);
        let spans: Vec<_> = lines[0]
            .spans
            .iter()
            .map(|span| (span.content.as_ref(), span.style))
            .collect();
        assert_eq!(
            spans,
            [
                ("● ", Style::new()),
                ("bold", Style::new().add_modifier(Modifier::BOLD)),
                (" ", Style::new()),
                ("code", Style::new().fg(theme::LIGHT_YELLOW)),
            ]
        );
        let dim =
            view(vec![thought("**bold** `code`")], now).lines(40, true, ToolOutput::Summary, now);
        for span in &dim[0].spans[1..] {
            assert_eq!(span.style.fg, Some(theme::DIM), "{:?}", span.content);
        }
        let output = ToolCall::new("r", "Read a.md")
            .name("read_file".to_owned())
            .status(ToolCallStatus::Completed)
            .content(vec![ToolCallContent::from(ContentBlock::from(
                "# not a heading",
            ))]);
        let view = self::view(
            vec![SessionUpdate::ToolCall(output), answer("a", "# heading")],
            now,
        );
        assert_eq!(
            text(&view.lines(40, false, ToolOutput::Full, now)),
            [
                "● Read a.md",
                "  └ # not a heading",
                "",
                "● Final answer from subagent child-1",
                "  └ heading",
            ],
            "named output stays plain and a nameless answer is Markdown"
        );
    }

    #[test]
    fn chunks_of_one_kind_join_one_item_and_the_next_update_ends_thinking() {
        let start = Instant::now();
        let at = |secs| start + Duration::from_secs(secs);
        let mut view = TranscriptView::default();
        view.update(thought("weigh"), at(0));
        view.update(thought("ing"), at(1));
        view.update(message("one "), at(3));
        view.update(message("two"), at(3));
        view.update(thought("again"), at(4));
        assert_eq!(
            rows(&view, 40, true, at(5)),
            ["● weighing", "", "● one two", "", "● again"]
        );
        assert_eq!(
            rows(&view, 40, false, at(5)),
            ["● Thought for 3s", "", "● one two", "", "● Thinking..."]
        );
        view.end_turn(at(6));
        view.update(thought("next"), at(7));
        assert_eq!(
            rows(&view, 40, false, at(8)),
            [
                "● Thought for 3s",
                "",
                "● one two",
                "",
                "● Thought for 2s",
                "",
                "● Thinking..."
            ]
        );
    }

    #[test]
    fn replay_keeps_user_messages_and_separates_their_responses() {
        let now = Instant::now();
        let user = |text: &str| SessionUpdate::UserMessageChunk(ContentChunk::new(text.into()));
        let view = view(
            vec![
                user("title"),
                SessionUpdate::SessionInfoUpdate(SessionInfoUpdate::new().title("saved")),
                user("first "),
                user("question"),
                message("first answer"),
                user("second question"),
                message("second answer"),
            ],
            now,
        );
        assert_eq!(
            rows(&view, 40, false, now),
            [
                "❯ title",
                "",
                "❯ first question",
                "",
                "● first answer",
                "",
                "❯ second question",
                "",
                "● second answer",
            ]
        );
    }

    #[test]
    fn reused_tool_ids_keep_each_turns_call_in_the_transcript() {
        let now = Instant::now();
        let mut view = TranscriptView::default();
        view.update(
            SessionUpdate::UserMessageChunk(ContentChunk::new("first".into())),
            now,
        );
        view.update(read("same", "first.txt"), now);
        view.update(
            SessionUpdate::UserMessageChunk(ContentChunk::new("second".into())),
            now,
        );
        view.update(read("same", "second.txt"), now);
        view.update(
            SessionUpdate::ToolCallUpdate(ToolCallUpdate::new(
                "same",
                ToolCallUpdateFields::new().status(ToolCallStatus::Failed),
            )),
            now,
        );
        assert_eq!(
            rows(&view, 40, false, now),
            [
                "❯ first",
                "",
                "● Read first.txt",
                "",
                "❯ second",
                "",
                "● Read second.txt",
            ]
        );
        let lines = view.lines(40, false, ToolOutput::Summary, now);
        assert_eq!(lines[2].spans[0].style.fg, Some(theme::GREEN));
        assert_eq!(lines[6].spans[0].style.fg, Some(theme::RED));

        view.end_turn(now);
        view.update(read("same", "third.txt"), now);
        assert_eq!(
            rows(&view, 40, false, now).last().unwrap(),
            "● Read third.txt"
        );
    }

    #[test]
    fn thinking_shows_placeholders_by_elapsed_time_unless_toggled() {
        let start = Instant::now();
        let at = |secs| start + Duration::from_secs(secs);
        let mut view = view(vec![thought("weighing it")], start);
        assert_eq!(rows(&view, 40, false, at(3)), ["● Thinking..."]);
        assert_eq!(rows(&view, 40, false, at(12)), ["● Thinking for 12s..."]);
        assert_eq!(rows(&view, 40, true, at(12)), ["● weighing it"]);
        view.end_turn(at(42));
        assert_eq!(rows(&view, 40, false, at(60)), ["● Thought for 42s"]);
        assert_eq!(rows(&view, 40, true, at(60)), ["● weighing it"]);
        for show in [false, true] {
            let line = &view.lines(40, show, ToolOutput::Summary, at(60))[0];
            assert_eq!(color(line), Some(theme::DIM), "{show}");
        }
        let empty = self::view(vec![thought("  ")], start);
        assert_eq!(rows(&empty, 40, true, at(3)), ["● Thinking..."]);
    }

    #[test]
    fn items_render_with_icons_prefixes_and_spacing() {
        let now = Instant::now();
        for (case, updates, width, expected) in [
            (
                "one-line tool call",
                vec![read("a", "Makefile")],
                40,
                vec!["● Read Makefile"],
            ),
            (
                "clipped with an ellipsis",
                vec![read("a", "tallies/2026/september/a.tally")],
                20,
                vec!["● Read tallies/2026…"],
            ),
            (
                "nameless tool call with wrapped content",
                vec![answer("a", "Fixed the tallies today.")],
                24,
                vec![
                    "● Final answer from sub…",
                    "  └ Fixed the tallies",
                    "    today.",
                ],
            ),
            (
                "every item is followed by a blank row",
                vec![
                    message("Checking"),
                    read("a", "a"),
                    shell("s", "ls -la", false, ToolCallStatus::Completed),
                    answer("c", "Fixed."),
                    read("d", "d"),
                    message("Found it"),
                ],
                40,
                vec![
                    "● Checking",
                    "",
                    "● Read a",
                    "",
                    "● Shell ls -la",
                    "",
                    "● Final answer from subagent child-1",
                    "  └ Fixed.",
                    "",
                    "● Read d",
                    "",
                    "● Found it",
                ],
            ),
        ] {
            assert_eq!(
                rows(&view(updates, now), width, false, now),
                expected,
                "{case}"
            );
        }
        let mut view = view(vec![answer("a", "Fixed.")], now);
        view.notice("Turn error: bad".to_owned(), theme::RED, now);
        view.notice("stderr line".to_owned(), theme::DIM, now);
        let lines = view.lines(40, false, ToolOutput::Summary, now);
        assert_eq!(
            rows(&view, 40, false, now)[2..],
            ["", "Turn error: bad", "", "stderr line"]
        );
        assert_eq!(color(&lines[1]), Some(theme::DIM));
        assert_eq!(color(&lines[3]), Some(theme::RED));
        assert_eq!(color(&lines[5]), Some(theme::DIM));
    }

    #[test]
    fn a_named_call_shows_its_text_and_diff_blocks_only_while_output_is_shown() {
        let now = Instant::now();
        let old = "fn main() {\n    let config = Config::load();\n    run(config)\n}\n";
        let new = "fn main() {\n    let config = Config::load()?;\n    run(config)\n}\n";
        let call = ToolCall::new("p", "Apply patch to a.rs")
            .name("apply_patch".to_owned())
            .status(ToolCallStatus::Completed)
            .content(vec![
                ToolCallContent::from(ContentBlock::from("Modified a.rs")),
                ToolCallContent::from(Diff::new("/w/a.rs", new).old_text(old.to_owned())),
            ]);
        let view = view(vec![SessionUpdate::ToolCall(call)], now);
        assert_eq!(rows(&view, 32, false, now), ["● Apply patch to a.rs"]);
        let lines = view.lines(32, false, ToolOutput::Full, now);
        assert_eq!(
            text(&lines),
            [
                "● Apply patch to a.rs",
                "  └ Modified a.rs",
                "    @@ -1,4 +1,4 @@",
                "     fn main() {",
                "    -    let config = Config::l…",
                "    +    let config = Config::l…",
                "         run(config)",
                "     }",
            ]
        );
        let colors: Vec<_> = lines[1..].iter().map(color).collect();
        assert_eq!(
            colors,
            [
                Some(theme::DIM),
                Some(theme::DIM),
                Some(theme::DIM),
                Some(theme::RED),
                Some(theme::GREEN),
                Some(theme::DIM),
                Some(theme::DIM),
            ]
        );
        let added = ToolCall::new("a", "Apply patch to b")
            .name("apply_patch".to_owned())
            .status(ToolCallStatus::Completed)
            .content(vec![ToolCallContent::from(Diff::new(
                "/w/b",
                "one\n\ttwo\n",
            ))]);
        let view = self::view(vec![SessionUpdate::ToolCall(added)], now);
        assert_eq!(
            text(&view.lines(40, false, ToolOutput::Full, now)),
            [
                "● Apply patch to b",
                "  └ @@ -0,0 +1,2 @@",
                "    +one",
                "    +        two",
            ],
            "a new file diffs against nothing and tabs are expanded"
        );
    }

    #[test]
    fn truncated_output_shows_the_hidden_row_count_and_the_last_five_rows() {
        let now = Instant::now();
        for (lines, expected) in [
            (
                52,
                vec![
                    "● Read a.txt",
                    "  └ ...47 more lines",
                    "    line 48",
                    "    line 49",
                    "    line 50",
                    "    line 51",
                    "    line 52",
                ],
            ),
            (
                6,
                vec![
                    "● Read a.txt",
                    "  └ line 1",
                    "    line 2",
                    "    line 3",
                    "    line 4",
                    "    line 5",
                    "    line 6",
                ],
            ),
        ] {
            let text: Vec<_> = (1..=lines).map(|n| format!("line {n}")).collect();
            let call = ToolCall::new("r", "Read a.txt")
                .name("read_file".to_owned())
                .status(ToolCallStatus::Completed)
                .content(vec![ToolCallContent::from(ContentBlock::from(
                    text.join("\n"),
                ))]);
            let view = view(vec![SessionUpdate::ToolCall(call)], now);
            let rows = view.lines(40, false, ToolOutput::Truncated, now);
            assert_eq!(self::text(&rows), expected, "{lines} lines");
            let colors: Vec<_> = rows[1..].iter().map(|row| row.spans[1].style.fg).collect();
            let first = if lines > 6 { theme::FAINT } else { theme::DIM };
            assert_eq!(colors[0], Some(first), "{lines} lines");
            assert!(
                colors[1..].iter().all(|&c| c == Some(theme::DIM)),
                "{lines} lines"
            );
        }
    }

    #[test]
    fn shell_calls_render_as_one_clipped_row() {
        let now = Instant::now();
        let pending = view(vec![shell("s", "ls", false, ToolCallStatus::Pending)], now);
        assert_eq!(rows(&pending, 40, false, now), ["● Shell ls"]);
        let running = view(
            vec![shell(
                "s",
                "rg -n \\\n  --glob '*.rs' \\\n  'TODO|FIXME' src\n",
                false,
                ToolCallStatus::InProgress,
            )],
            now,
        );
        assert_eq!(
            rows(&running, 40, false, now),
            ["● Shell rg -n \\ --glob '*.rs' \\ 'TODO|F…"]
        );
        let background = view(
            vec![shell("s", "npm run dev", true, ToolCallStatus::Completed)],
            now,
        );
        assert_eq!(rows(&background, 40, false, now), ["● Shell npm run dev &"]);
        let clipped = view(
            vec![shell(
                "s",
                "echo abcdefghij",
                false,
                ToolCallStatus::Completed,
            )],
            now,
        );
        assert_eq!(rows(&clipped, 14, false, now), ["● Shell echo …"]);
        let failed = view(
            vec![shell("s", "rm -rf x", false, ToolCallStatus::Failed)],
            now,
        );
        assert_eq!(rows(&failed, 40, false, now), ["● Shell rm -rf x"]);
        assert_eq!(
            failed.lines(40, false, ToolOutput::Summary, now)[0].spans[0]
                .style
                .fg,
            Some(theme::RED)
        );
    }

    #[test]
    fn the_tool_status_sets_the_icon_color() {
        let now = Instant::now();
        for (status, color) in [
            (ToolCallStatus::Pending, Some(theme::YELLOW)),
            (ToolCallStatus::InProgress, None),
            (ToolCallStatus::Completed, Some(theme::GREEN)),
            (ToolCallStatus::Failed, Some(theme::RED)),
        ] {
            let call = ToolCall::new("a", "Read a")
                .name("read_file".to_owned())
                .status(status);
            let view = view(vec![SessionUpdate::ToolCall(call)], now);
            let line = &view.lines(40, false, ToolOutput::Summary, now)[0];
            assert_eq!(line.spans[0].content, "●");
            assert_eq!(line.spans[0].style.fg, color, "{status:?}");
        }
    }

    #[test]
    fn paging_turns_auto_scroll_off_and_end_or_the_bottom_turns_it_on() {
        let now = Instant::now();
        let mut view = TranscriptView::default();
        let (height, total) = (10, 35);
        view.page_up(height, 5);
        assert_eq!(view.first_row(height, 5), 0);
        assert!(view.top.is_none(), "nothing to scroll");
        view.page_up(height, total);
        assert_eq!(view.first_row(height, total), 16);
        view.update(message("more"), now);
        assert!(view.new_activity);
        view.page_up(height, total);
        view.page_up(height, total);
        assert_eq!(view.first_row(height, total), 0);
        view.page_down(height, total);
        assert_eq!(view.first_row(height, total), 9);
        assert!(view.new_activity);
        view.page_down(height, total);
        view.page_down(height, total);
        assert_eq!(view.first_row(height, total), 25);
        assert!(view.top.is_none());
        assert!(!view.new_activity);
        view.page_up(height, total);
        view.changed();
        assert!(view.new_activity);
        view.end();
        assert!(view.top.is_none());
        assert!(!view.new_activity);
        view.update(message("later"), now);
        assert!(!view.new_activity);
    }
}
