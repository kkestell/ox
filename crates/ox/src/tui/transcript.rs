use std::collections::HashMap;
use std::time::Instant;

use agent_client_protocol::schema::v1::*;
use ratatui::style::{Color, Style};
use ratatui::text::{Line, Span};
use serde_json::Value;
use unicode_width::{UnicodeWidthChar, UnicodeWidthStr};

use super::escape;

/// Seconds before the thinking placeholder starts counting.
const COUNT_AFTER: u64 = 10;

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
    /// The manual top row while auto-scroll is off.
    top: Option<usize>,
    pub new_activity: bool,
}

impl TranscriptView {
    pub fn user(&mut self, text: String, now: Instant) {
        self.end_thinking(now);
        self.items.push(Item::User(text));
        self.changed();
    }

    pub fn notice(&mut self, text: String, color: Color, now: Instant) {
        self.end_thinking(now);
        self.items.push(Item::Notice { text, color });
        self.changed();
    }

    pub fn end_turn(&mut self, now: Instant) {
        self.end_thinking(now);
    }

    pub fn update(&mut self, update: SessionUpdate, now: Instant) {
        match update {
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

    pub fn lines(&self, width: usize, show_thinking: bool, now: Instant) -> Vec<Line<'static>> {
        let mut lines = Vec::new();
        let mut previous_bullet = None;
        for item in &self.items {
            let (item_lines, bullet) = item_lines(item, width, show_thinking, now);
            if item_lines.is_empty() {
                continue;
            }
            if let Some(previous) = previous_bullet
                && !(previous && bullet)
            {
                lines.push(Line::default());
            }
            lines.extend(item_lines);
            previous_bullet = Some(bullet);
        }
        lines
    }
}

fn page(height: usize) -> usize {
    height.saturating_sub(1).max(1)
}

fn gray() -> Style {
    Style::new().fg(Color::DarkGray)
}

/// An item's rows and whether they are one `•` line.
fn item_lines(
    item: &Item,
    width: usize,
    show_thinking: bool,
    now: Instant,
) -> (Vec<Line<'static>>, bool) {
    let lines = match item {
        Item::User(text) => prefixed(text, width, "❯ ", Style::new()),
        Item::Thinking {
            text,
            started,
            ended,
        } => {
            if show_thinking && !text.trim().is_empty() {
                styled(wrap(text, width), gray())
            } else {
                vec![Line::styled(placeholder(*started, *ended, now), gray())]
            }
        }
        Item::Response(text) => styled(wrap(text, width), Style::new()),
        Item::Tool(call) => {
            return match shell_lines(call, width) {
                Some(_) if call.status == ToolCallStatus::Pending => (Vec::new(), false),
                Some(lines) => (lines, false),
                None if call.name.is_some() => (vec![tool_line(call, width)], true),
                None => {
                    let lines = described_lines(call, width);
                    let bullet = lines.len() == 1;
                    (lines, bullet)
                }
            };
        }
        Item::Notice { text, color } => styled(wrap(text, width), Style::new().fg(*color)),
    };
    (lines, false)
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

/// The rows an approval shows for a request: its `$` block, or its `•` line
/// and content.
pub fn call_lines(call: &ToolCall, width: usize) -> Vec<Line<'static>> {
    shell_lines(call, width).unwrap_or_else(|| described_lines(call, width))
}

fn described_lines(call: &ToolCall, width: usize) -> Vec<Line<'static>> {
    let mut lines = vec![tool_line(call, width)];
    lines.extend(prefixed(&tool_content(&call.content), width, "  ", gray()));
    lines
}

fn icon(call: &ToolCall, symbol: &str) -> Span<'static> {
    let style = match call.status {
        ToolCallStatus::Pending => Style::new().fg(Color::Indexed(208)),
        ToolCallStatus::Failed => Style::new().fg(Color::Red),
        _ => Style::new(),
    };
    Span::styled(symbol.to_owned(), style)
}

fn tool_line(call: &ToolCall, width: usize) -> Line<'static> {
    Line::from(vec![
        icon(call, "•"),
        Span::raw(" "),
        Span::raw(clip(&call.title, width.saturating_sub(2))),
    ])
}

/// The `$` block of a shell call, or nothing when the call is not a shell
/// command.
fn shell_lines(call: &ToolCall, width: usize) -> Option<Vec<Line<'static>>> {
    if call.name.as_deref() != Some("shell") {
        return None;
    }
    let input = call.raw_input.as_ref()?;
    let command = input.get("command")?.as_str()?;
    let background = input
        .get("background")
        .and_then(Value::as_bool)
        .unwrap_or(false);
    let mut rows: Vec<String> = command
        .trim()
        .split('\n')
        .map(|line| expand(&escape(line)))
        .collect();
    if background {
        rows.last_mut().expect("split yields a row").push_str(" &");
    }
    let text_width = width.saturating_sub(2).max(1);
    let mut lines = Vec::new();
    for (index, row) in rows.iter().enumerate() {
        for (piece, text) in hard_wrap(row, text_width).into_iter().enumerate() {
            if index == 0 && piece == 0 {
                lines.push(Line::from(vec![
                    icon(call, "$"),
                    Span::raw(" "),
                    Span::raw(text),
                ]));
            } else {
                lines.push(Line::from(format!("  {text}")));
            }
        }
    }
    Some(lines)
}

/// Wrapped rows with `prefix` before the first and two spaces before the rest.
fn prefixed(text: &str, width: usize, prefix: &str, style: Style) -> Vec<Line<'static>> {
    wrap(text, width.saturating_sub(2))
        .into_iter()
        .enumerate()
        .map(|(index, row)| {
            let prefix = if index == 0 { prefix } else { "  " };
            Line::styled(format!("{prefix}{row}"), style)
        })
        .collect()
}

fn styled(rows: Vec<String>, style: Style) -> Vec<Line<'static>> {
    rows.into_iter()
        .map(|row| Line::styled(row, style))
        .collect()
}

fn content(block: &ContentBlock) -> String {
    match block {
        ContentBlock::Text(text) => text.text.clone(),
        _ => "[non-text content]".to_owned(),
    }
}

fn tool_content(contents: &[ToolCallContent]) -> String {
    contents
        .iter()
        .map(|item| match item {
            ToolCallContent::Content(item) => content(&item.content),
            _ => "[non-text content]".to_owned(),
        })
        .collect::<Vec<_>>()
        .join("\n")
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

/// Splits a row by display width without regard to words.
fn hard_wrap(row: &str, width: usize) -> Vec<String> {
    let mut rows = Vec::new();
    let mut current = String::new();
    let mut column = 0;
    for c in row.chars() {
        let c_width = c.width().unwrap_or(0);
        if column + c_width > width && column > 0 {
            rows.push(std::mem::take(&mut current));
            column = 0;
        }
        current.push(c);
        column += c_width;
    }
    rows.push(current);
    rows
}

/// Word-wraps escaped text by display width. The whole text is trimmed,
/// interior blank lines are kept, tabs are expanded, and words wider than a
/// row are split.
pub fn wrap(text: &str, width: usize) -> Vec<String> {
    let width = width.max(1);
    let text = escape(text);
    let text = text.trim();
    if text.is_empty() {
        return Vec::new();
    }
    let mut rows = Vec::new();
    for line in text.split('\n') {
        let line = expand(line.trim_end());
        let mut current = String::new();
        let mut column = 0;
        let mut chars = line.chars().peekable();
        loop {
            let mut space = String::new();
            while let Some(c) = chars.next_if(|c| c.is_whitespace()) {
                space.push(c);
            }
            let mut word = String::new();
            while let Some(c) = chars.next_if(|c| !c.is_whitespace()) {
                word.push(c);
            }
            if space.is_empty() && word.is_empty() {
                break;
            }
            if column > 0 && column + space.width() + word.width() > width {
                rows.push(std::mem::take(&mut current));
                column = 0;
                space.clear();
            }
            for c in space.chars().chain(word.chars()) {
                let c_width = c.width().unwrap_or(0);
                if column + c_width > width && column > 0 {
                    rows.push(std::mem::take(&mut current));
                    column = 0;
                }
                current.push(c);
                column += c_width;
            }
        }
        rows.push(current);
    }
    rows
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

    fn rows(view: &TranscriptView, width: usize, show: bool, now: Instant) -> Vec<String> {
        view.lines(width, show, now)
            .iter()
            .map(ToString::to_string)
            .collect()
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
        for (case, user, text, width, expected) in [
            (
                "surrounding whitespace",
                false,
                "  \n\t hello world \n\n ",
                40,
                vec!["hello world"],
            ),
            ("whitespace only", false, " \n\t   ", 40, vec![]),
            (
                "interior blank lines",
                false,
                "one\n\n\ntwo\n",
                40,
                vec!["one", "", "", "two"],
            ),
            (
                "explicit newlines",
                false,
                "a\nb  \n c",
                40,
                vec!["a", "b", " c"],
            ),
            (
                "tabs",
                false,
                "a\tb\t\tc",
                40,
                vec!["a       b               c"],
            ),
            (
                "word wrap",
                false,
                "one two three four",
                10,
                vec!["one two", "three four"],
            ),
            (
                "long word",
                false,
                "ab abcdefgh",
                6,
                vec!["ab", "abcdef", "gh"],
            ),
            (
                "display width",
                false,
                "界界 界界界 界界界界",
                7,
                vec!["界界", "界界界", "界界界", "界"],
            ),
            (
                "escaped control characters",
                false,
                "a\rb \x1b",
                20,
                vec!["a\\rb \\u{1b}"],
            ),
            (
                "user prefix and indent",
                true,
                "  first line of text\nsecond  ",
                12,
                vec!["❯ first line", "  of text", "  second"],
            ),
        ] {
            let mut view = TranscriptView::default();
            if user {
                view.user(text.to_owned(), now);
            } else {
                view.update(message(text), now);
            }
            assert_eq!(rows(&view, width, false, now), expected, "{case}");
        }
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
            ["weighing", "", "one two", "", "again"]
        );
        assert_eq!(
            rows(&view, 40, false, at(5)),
            ["Thought for 3s", "", "one two", "", "Thinking..."]
        );
        view.end_turn(at(6));
        view.update(thought("next"), at(7));
        assert_eq!(
            rows(&view, 40, false, at(8)),
            [
                "Thought for 3s",
                "",
                "one two",
                "",
                "Thought for 2s",
                "",
                "Thinking..."
            ]
        );
    }

    #[test]
    fn thinking_shows_placeholders_by_elapsed_time_unless_toggled() {
        let start = Instant::now();
        let at = |secs| start + Duration::from_secs(secs);
        let mut view = view(vec![thought("weighing it")], start);
        assert_eq!(rows(&view, 40, false, at(3)), ["Thinking..."]);
        assert_eq!(rows(&view, 40, false, at(12)), ["Thinking for 12s..."]);
        assert_eq!(rows(&view, 40, true, at(12)), ["weighing it"]);
        view.end_turn(at(42));
        assert_eq!(rows(&view, 40, false, at(60)), ["Thought for 42s"]);
        assert_eq!(rows(&view, 40, true, at(60)), ["weighing it"]);
        for show in [false, true] {
            let line = &view.lines(40, show, at(60))[0];
            assert_eq!(color(line), Some(Color::DarkGray), "{show}");
        }
        let empty = self::view(vec![thought("  ")], start);
        assert_eq!(rows(&empty, 40, true, at(3)), ["Thinking..."]);
    }

    #[test]
    fn items_render_with_icons_prefixes_and_spacing() {
        let now = Instant::now();
        for (case, updates, width, expected) in [
            (
                "one-line tool call",
                vec![read("a", "Makefile")],
                40,
                vec!["• Read Makefile"],
            ),
            (
                "clipped with an ellipsis",
                vec![read("a", "tallies/2026/september/a.tally")],
                20,
                vec!["• Read tallies/2026…"],
            ),
            (
                "nameless tool call with wrapped content",
                vec![answer("a", "Fixed the tallies today.")],
                24,
                vec![
                    "• Final answer from sub…",
                    "  Fixed the tallies",
                    "  today.",
                ],
            ),
            (
                "adjacent bullets share no blank row",
                vec![
                    message("Checking"),
                    read("a", "a"),
                    read("b", "b"),
                    answer("c", "Fixed."),
                    read("d", "d"),
                    message("Found it"),
                ],
                40,
                vec![
                    "Checking",
                    "",
                    "• Read a",
                    "• Read b",
                    "",
                    "• Final answer from subagent child-1",
                    "  Fixed.",
                    "",
                    "• Read d",
                    "",
                    "Found it",
                ],
            ),
            (
                "a shell block has blank rows around it",
                vec![
                    read("a", "a"),
                    shell("s", "ls -la", false, ToolCallStatus::Completed),
                    read("b", "b"),
                ],
                40,
                vec!["• Read a", "", "$ ls -la", "", "• Read b"],
            ),
        ] {
            assert_eq!(
                rows(&view(updates, now), width, false, now),
                expected,
                "{case}"
            );
        }
        let mut view = view(vec![answer("a", "Fixed.")], now);
        view.notice("Turn error: bad".to_owned(), Color::Red, now);
        view.notice("stderr line".to_owned(), Color::DarkGray, now);
        let lines = view.lines(40, false, now);
        assert_eq!(
            rows(&view, 40, false, now)[2..],
            ["", "Turn error: bad", "", "stderr line"]
        );
        assert_eq!(color(&lines[1]), Some(Color::DarkGray));
        assert_eq!(color(&lines[3]), Some(Color::Red));
        assert_eq!(color(&lines[5]), Some(Color::DarkGray));
    }

    #[test]
    fn shell_calls_stay_hidden_while_pending_and_render_as_command_blocks() {
        let now = Instant::now();
        let pending = view(vec![shell("s", "ls", false, ToolCallStatus::Pending)], now);
        assert!(rows(&pending, 40, false, now).is_empty());
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
            ["$ rg -n \\", "    --glob '*.rs' \\", "    'TODO|FIXME' src"]
        );
        let background = view(
            vec![shell("s", "npm run dev", true, ToolCallStatus::Completed)],
            now,
        );
        assert_eq!(rows(&background, 40, false, now), ["$ npm run dev &"]);
        let wrapped = view(
            vec![shell(
                "s",
                "echo abcdefghij",
                false,
                ToolCallStatus::Completed,
            )],
            now,
        );
        assert_eq!(rows(&wrapped, 10, false, now), ["$ echo abc", "  defghij"]);
        let failed = view(
            vec![shell("s", "rm -rf x", false, ToolCallStatus::Failed)],
            now,
        );
        assert_eq!(rows(&failed, 40, false, now), ["$ rm -rf x"]);
        assert_eq!(
            failed.lines(40, false, now)[0].spans[0].style.fg,
            Some(Color::Red)
        );
    }

    #[test]
    fn the_tool_status_sets_the_icon_color() {
        let now = Instant::now();
        for (status, color) in [
            (ToolCallStatus::Pending, Some(Color::Indexed(208))),
            (ToolCallStatus::InProgress, None),
            (ToolCallStatus::Completed, None),
            (ToolCallStatus::Failed, Some(Color::Red)),
        ] {
            let call = ToolCall::new("a", "Read a")
                .name("read_file".to_owned())
                .status(status);
            let view = view(vec![SessionUpdate::ToolCall(call)], now);
            let line = &view.lines(40, false, now)[0];
            assert_eq!(line.spans[0].content, "•");
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
