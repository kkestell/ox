use std::collections::HashMap;
use std::io::{Write, stdout};

use agent_client_protocol::schema::v1::*;
use crossterm::{
    cursor::{Hide, MoveToColumn, Show},
    event::{
        DisableBracketedPaste, DisableFocusChange, EnableBracketedPaste, EnableFocusChange, Event,
        EventStream, KeyCode, KeyEventKind, KeyModifiers,
    },
    execute, queue,
    style::{Color, Print, ResetColor, SetForegroundColor},
    terminal::{self, Clear, ClearType},
};
use futures::StreamExt;
use tokio::sync::mpsc::UnboundedReceiver;
use unicode_width::UnicodeWidthChar;

use crate::acp::{self, Session};

pub fn escape(text: &str) -> String {
    text.chars()
        .flat_map(|c| {
            if c.is_control() && c != '\n' && c != '\t' {
                c.escape_default().collect::<Vec<_>>()
            } else {
                vec![c]
            }
        })
        .collect()
}

#[derive(Default)]
struct Input(String);

impl Input {
    fn edit(&mut self, event: Event) -> Option<String> {
        match event {
            Event::Paste(text) => self
                .0
                .push_str(&text.replace("\r\n", "\n").replace('\r', "\n")),
            Event::Key(key) if key.kind != KeyEventKind::Release => match key.code {
                KeyCode::Char('u') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                    self.0.clear()
                }
                KeyCode::Char(c)
                    if !key
                        .modifiers
                        .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT) =>
                {
                    self.0.push(c)
                }
                KeyCode::Backspace => {
                    self.0.pop();
                }
                KeyCode::Enter => return Some(std::mem::take(&mut self.0)),
                _ => {}
            },
            _ => {}
        }
        None
    }

    fn visible(&self, columns: u16) -> String {
        let text = escape(&self.0).replace('\n', "↵").replace('\t', "→");
        let mut width = 0;
        text.chars()
            .rev()
            .take_while(|c| {
                width += c.width().unwrap_or(0);
                width <= usize::from(columns)
            })
            .collect::<Vec<_>>()
            .into_iter()
            .rev()
            .collect()
    }
}

struct Terminal {
    input_drawn: bool,
    line_start: bool,
    // Stays true in terminals that never report focus, so they get no bells or unseen results.
    focused: bool,
    title: String,
}

fn restore() {
    // An empty title lets the tmux pane border show the pane's command again.
    let _ = write!(stdout(), "\x1b]2;\x1b\\");
    let _ = execute!(
        stdout(),
        ResetColor,
        DisableFocusChange,
        DisableBracketedPaste,
        Show
    );
    let _ = terminal::disable_raw_mode();
}

impl Terminal {
    fn enter() -> anyhow::Result<Self> {
        terminal::enable_raw_mode()?;
        let terminal = Self {
            input_drawn: false,
            line_start: true,
            focused: true,
            title: String::new(),
        };
        execute!(stdout(), EnableBracketedPaste, EnableFocusChange, Show)?;
        Ok(terminal)
    }

    fn erase(&mut self) -> anyhow::Result<()> {
        if self.input_drawn {
            execute!(stdout(), MoveToColumn(0), Clear(ClearType::CurrentLine))?;
            self.input_drawn = false;
        }
        Ok(())
    }

    fn write(&mut self, spans: &[Span]) -> anyhow::Result<()> {
        self.erase()?;
        for span in spans.iter().filter(|span| !span.text.is_empty()) {
            let text = escape(&span.text).replace('\n', "\r\n");
            if span.reasoning {
                queue!(
                    stdout(),
                    SetForegroundColor(Color::DarkGrey),
                    Print(text),
                    ResetColor
                )?;
            } else {
                write!(stdout(), "{text}")?;
            }
            self.line_start = span.text.ends_with('\n');
        }
        stdout().flush()?;
        Ok(())
    }

    fn print(&mut self, text: &str) -> anyhow::Result<()> {
        self.write(&[Span {
            text: text.to_owned(),
            reasoning: false,
        }])
    }

    fn newline(&mut self) -> anyhow::Result<()> {
        if !self.line_start {
            self.print("\n")?;
        }
        Ok(())
    }

    fn status(&mut self, status: &str) -> anyhow::Result<()> {
        let title = format!("ox: {status}");
        if title != self.title {
            write!(stdout(), "\x1b]2;{title}\x1b\\")?;
            stdout().flush()?;
            self.title = title;
        }
        Ok(())
    }

    fn bell(&mut self) -> anyhow::Result<()> {
        if !self.focused {
            write!(stdout(), "\x07")?;
            stdout().flush()?;
        }
        Ok(())
    }

    fn draw(&mut self, input: &Input, enabled: bool, permission: bool) -> anyhow::Result<()> {
        self.erase()?;
        if enabled {
            self.newline()?;
            let width = terminal::size()?.0;
            // Leave the final column unused to prevent automatic line wrapping.
            let prefix = if permission { "? " } else { "> " };
            let prefix = &prefix[..usize::from(width.saturating_sub(1)).min(2)];
            write!(
                stdout(),
                "{prefix}{}",
                input.visible(width.saturating_sub(prefix.len() as u16 + 1))
            )?;
            execute!(stdout(), Show)?;
            self.input_drawn = true;
        } else {
            execute!(stdout(), Hide)?;
        }
        stdout().flush()?;
        Ok(())
    }
}

impl Drop for Terminal {
    fn drop(&mut self) {
        let _ = self.erase();
        let _ = self.newline();
        restore();
    }
}

/// Transcript text written in one style.
#[derive(Debug, PartialEq)]
struct Span {
    text: String,
    reasoning: bool,
}

fn push(out: &mut Vec<Span>, reasoning: bool, text: &str) {
    match out.last_mut() {
        Some(span) if span.reasoning == reasoning => span.text.push_str(text),
        _ => out.push(Span {
            text: text.to_owned(),
            reasoning,
        }),
    }
}

fn width(text: &str) -> usize {
    text.chars().map(|c| c.width().unwrap_or(0)).sum()
}

/// Replaces tabs with spaces up to the next tab stop.
fn expand(space: &str, mut column: usize) -> String {
    let mut text = String::new();
    for c in space.chars() {
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

#[derive(Clone, Copy, Debug, PartialEq)]
enum Kind {
    User,
    Reasoning,
    Response,
}

/// A message being written. Whitespace and an unfinished word wait until later
/// text shows whether they are interior or trailing.
struct Message {
    kind: Kind,
    /// Whether a blank line precedes the first word.
    blank: bool,
    started: bool,
    space: String,
    word: String,
    column: usize,
}

impl Message {
    fn new(kind: Kind, blank: bool) -> Self {
        Self {
            kind,
            blank,
            started: false,
            space: String::new(),
            word: String::new(),
            column: 0,
        }
    }

    fn push(&mut self, text: &str, columns: u16, out: &mut Vec<Span>) {
        // Escape first so widths match what the terminal shows.
        for c in escape(text).chars() {
            if !c.is_whitespace() {
                self.word.push(c);
                continue;
            }
            if !self.word.is_empty() {
                self.place(columns, out);
            }
            if self.started {
                self.space.push(c);
            }
        }
    }

    /// Writes the last word and discards trailing whitespace. Returns whether
    /// the message wrote anything.
    fn finish(mut self, columns: u16, out: &mut Vec<Span>) -> bool {
        if !self.word.is_empty() {
            self.place(columns, out);
        }
        if self.started {
            push(out, false, "\n");
        }
        self.started
    }

    /// Writes the whitespace before the completed word and the word, wrapping
    /// before the word when it does not fit and splitting a word wider than a
    /// line.
    fn place(&mut self, columns: u16, out: &mut Vec<Span>) {
        // Leave the final column unused to prevent automatic line wrapping.
        let limit = usize::from(columns).saturating_sub(1);
        let indent = if self.kind == Kind::User { 2 } else { 0 };
        let mut text = String::new();
        if !self.started {
            self.started = true;
            if self.blank {
                text.push('\n');
            }
            if self.kind == Kind::User {
                text.push_str("> ");
                self.column = indent;
            }
        }
        let space = std::mem::take(&mut self.space);
        let mut lines = space.split('\n');
        let mut space = lines.next().unwrap_or_default();
        for line in lines {
            text.push('\n');
            self.column = 0;
            space = line;
        }
        if self.column == 0 {
            text.push_str(&" ".repeat(indent));
            self.column = indent;
        }
        let space = expand(space, self.column);
        let word = std::mem::take(&mut self.word);
        if self.column + width(&space) + width(&word) <= limit {
            text.push_str(&space);
            self.column += width(&space);
        } else if self.column > indent {
            text.push('\n');
            text.push_str(&" ".repeat(indent));
            self.column = indent;
        }
        for c in word.chars() {
            let c_width = c.width().unwrap_or(0);
            if self.column + c_width > limit && self.column > indent {
                text.push('\n');
                text.push_str(&" ".repeat(indent));
                self.column = indent;
            }
            text.push(c);
            self.column += c_width;
        }
        push(out, self.kind == Kind::Reasoning, &text);
    }
}

/// Escapes control characters and clips the line with `...`, leaving the
/// final column unused.
fn clip(line: &str, columns: u16) -> String {
    let line: String = line
        .chars()
        .map(|c| {
            if c.is_control() {
                c.escape_default().to_string()
            } else {
                c.to_string()
            }
        })
        .collect();
    let limit = usize::from(columns).saturating_sub(1);
    if width(&line) <= limit {
        return line;
    }
    let mut clipped = String::new();
    let mut used = 0;
    for c in line.chars() {
        used += c.width().unwrap_or(0);
        if used + 3 > limit {
            break;
        }
        clipped.push(c);
    }
    clipped.push_str(&"..."[..limit.min(3)]);
    clipped
}

struct Tool {
    call: ToolCall,
    /// Whether its transcript line has been written.
    shown: bool,
}

#[derive(Default)]
struct Output {
    tools: HashMap<ToolCallId, Tool>,
    message: Option<Message>,
    /// Whether the last block written was a tool call line, which the next
    /// one follows without a blank line.
    after_tool: bool,
}

impl Output {
    fn merge(&mut self, update: ToolCallUpdate) -> &mut Tool {
        let id = update.tool_call_id;
        let tool = self.tools.entry(id.clone()).or_insert_with(|| Tool {
            call: ToolCall::new(id, "Tool"),
            shown: false,
        });
        tool.call.update(update.fields);
        tool
    }

    fn chunk(&mut self, kind: Kind, text: &str, columns: u16, out: &mut Vec<Span>) {
        if self
            .message
            .as_ref()
            .is_none_or(|message| message.kind != kind)
        {
            self.end(columns, out);
            self.message = Some(Message::new(kind, kind != Kind::User));
        }
        self.message
            .as_mut()
            .expect("a message was just started")
            .push(text, columns, out);
    }

    fn end(&mut self, columns: u16, out: &mut Vec<Span>) {
        if let Some(message) = self.message.take()
            && message.finish(columns, out)
        {
            self.after_tool = false;
        }
    }

    /// Finishes the current message before other output.
    fn finish(&mut self, columns: u16) -> Vec<Span> {
        let mut out = Vec::new();
        self.end(columns, &mut out);
        self.after_tool = false;
        out
    }

    fn user(&mut self, text: &str, columns: u16) -> Vec<Span> {
        let mut out = Vec::new();
        self.chunk(Kind::User, text, columns, &mut out);
        self.end(columns, &mut out);
        out
    }

    /// Writes a tool call's line the first time it is updated. A model tool
    /// call shows its name and arguments; anything else shows its tool call
    /// title. One that arrives finished, such as a subagent answer, also shows
    /// its content.
    fn show(&mut self, id: &ToolCallId, columns: u16, out: &mut Vec<Span>) {
        let tool = self
            .tools
            .get_mut(id)
            .expect("the tool call was just updated");
        if tool.shown {
            return;
        }
        tool.shown = true;
        let call = &tool.call;
        let (line, details) = match (&call.name, &call.raw_input) {
            (Some(name), Some(input)) => {
                let input = serde_json::to_string(input).expect("JSON values serialize");
                let padding = " ".repeat(16usize.saturating_sub(width(name)));
                (format!("{name}{padding} {input}"), String::new())
            }
            _ if matches!(
                call.status,
                ToolCallStatus::Completed | ToolCallStatus::Failed
            ) =>
            {
                (call.title.clone(), tool_content(&call.content))
            }
            _ => (call.title.clone(), String::new()),
        };
        self.end(columns, out);
        if !self.after_tool {
            push(out, false, "\n");
        }
        push(out, false, &format!("{}\n", clip(&line, columns)));
        self.after_tool = true;
        let mut message = Message::new(Kind::Response, false);
        message.push(&details, columns, out);
        if message.finish(columns, out) {
            self.after_tool = false;
        }
    }

    fn update(&mut self, update: SessionUpdate, columns: u16) -> Vec<Span> {
        let mut out = Vec::new();
        match update {
            SessionUpdate::AgentMessageChunk(chunk) => {
                self.chunk(Kind::Response, &content(&chunk.content), columns, &mut out)
            }
            SessionUpdate::AgentThoughtChunk(chunk) => {
                self.chunk(Kind::Reasoning, &content(&chunk.content), columns, &mut out)
            }
            SessionUpdate::ToolCall(call) => {
                let id = call.tool_call_id.clone();
                let shown = self.tools.get(&id).is_some_and(|tool| tool.shown);
                self.tools.insert(id.clone(), Tool { call, shown });
                self.show(&id, columns, &mut out);
            }
            SessionUpdate::ToolCallUpdate(update) => {
                let id = update.tool_call_id.clone();
                self.merge(update);
                self.show(&id, columns, &mut out);
            }
            _ => {}
        }
        out
    }

    fn permission(&mut self, request: &RequestPermissionRequest, columns: u16) -> Vec<Span> {
        let mut out = self.finish(columns);
        let tool = &self.merge(request.tool_call.clone()).call;
        let mut text = format!("\n{}\n{}", tool.title, tool_content(&tool.content));
        text.push_str("Permission required\n");
        for (index, option) in request.options.iter().enumerate() {
            text.push_str(&format!("{}. {}\n", index + 1, option.name));
        }
        push(&mut out, false, &text);
        out
    }
}

fn content(block: &ContentBlock) -> String {
    match block {
        ContentBlock::Text(text) => text.text.clone(),
        _ => "[non-text content]".into(),
    }
}

fn tool_content(contents: &[ToolCallContent]) -> String {
    let mut text = String::new();
    for item in contents {
        match item {
            ToolCallContent::Content(item) => text.push_str(&content(&item.content)),
            ToolCallContent::Diff(diff) => {
                text.push_str(&format!("{}\n", diff.path.display()));
                if let Some(old) = &diff.old_text {
                    text.push_str(old);
                    text.push('\n');
                }
                text.push_str(&diff.new_text);
            }
            _ => text.push_str("[non-text content]"),
        }
        text.push('\n');
    }
    text
}

fn columns() -> anyhow::Result<u16> {
    Ok(terminal::size()?.0)
}

pub async fn run(
    mut session: Session,
    mut events: UnboundedReceiver<acp::Event>,
) -> anyhow::Result<()> {
    let previous = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        restore();
        previous(info);
    }));
    let mut terminal = Terminal::enter()?;
    let mut keys = EventStream::new();
    let mut input = Input::default();
    let mut output = Output::default();
    // The result of the last turn when it ended while the terminal was unfocused.
    let mut unseen = None;
    terminal
        .print("Enter: send · Ctrl-U: clear · Ctrl-C: cancel / clear / quit · Ctrl-D: quit\n")?;
    loop {
        terminal.status(if !session.pending.is_empty() {
            "needs permission"
        } else if session.busy {
            "working"
        } else {
            unseen.unwrap_or("ready")
        })?;
        terminal.draw(
            &input,
            !session.busy || !session.pending.is_empty(),
            !session.pending.is_empty(),
        )?;
        tokio::select! {
            biased;
            _ = session.closed() => anyhow::bail!("server closed the ACP connection"),
            Some(event) = events.recv() => match event {
                acp::Event::Update(update) => terminal.write(&output.update(update, columns()?))?,
                acp::Event::Diagnostic(text) => {
                    terminal.write(&output.finish(columns()?))?;
                    terminal.newline()?;
                    terminal.print(&format!("{text}\n"))?;
                }
                acp::Event::Permission(request, responder) => {
                    let first = session.pending.is_empty();
                    session.permission(request, responder)?;
                    if first && let Some((request, _)) = session.pending.front() {
                        input.0.clear();
                        terminal.write(&output.permission(request, columns()?))?;
                        terminal.bell()?;
                    }
                }
                acp::Event::Finished(result) => {
                    session.finished()?;
                    input.0.clear();
                    terminal.write(&output.finish(columns()?))?;
                    terminal.newline()?;
                    unseen = (!terminal.focused).then_some(if result.is_err() { "turn error" } else { "finished" });
                    terminal.bell()?;
                    if let Err(error) = result { terminal.print(&format!("Turn error: {error}\n"))?; }
                    else { terminal.print("Turn finished\n")?; }
                }
            },
            event = keys.next() => {
                let Some(event) = event else { session.cancel()?; return Ok(()) };
                let event = event?;
                match event {
                    Event::FocusGained => { terminal.focused = true; unseen = None; continue; }
                    Event::FocusLost => { terminal.focused = false; continue; }
                    _ => {}
                }
                if let Event::Key(key) = event
                    && key.kind != KeyEventKind::Release
                    && key.modifiers.contains(KeyModifiers::CONTROL) {
                        match key.code {
                            KeyCode::Char('d') => { session.cancel()?; return Ok(()) }
                            KeyCode::Char('c') => {
                                if session.busy {
                                    session.cancel()?;
                                    input.0.clear();
                                    terminal.write(&output.finish(columns()?))?;
                                    terminal.newline()?;
                                    terminal.print("Cancelling…\n")?;
                                }
                                else if input.0.is_empty() { return Ok(()) }
                                else { input.0.clear(); }
                                continue;
                            }
                            _ => {}
                        }
                    }
                if (!session.busy || !session.pending.is_empty())
                    && let Some(text) = input.edit(event) {
                        if session.pending.is_empty() {
                            if !text.is_empty() {
                                terminal.write(&output.user(&text, columns()?))?;
                                session.prompt(text)?;
                            }
                        } else if !session.answer(text.trim().parse().unwrap_or(0))? {
                            terminal.print("Enter one of the supplied option numbers\n")?;
                        } else if let Some((request, _)) = session.pending.front() {
                            terminal.write(&output.permission(request, columns()?))?;
                        }
                    }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crossterm::event::KeyEvent;
    use serde_json::Value;
    use unicode_width::UnicodeWidthStr;

    #[test]
    fn server_control_characters_are_printed_as_text() {
        assert_eq!(
            escape("\x1b[2J\r\x07\u{009b}31m\n\t"),
            "\\u{1b}[2J\\r\\u{7}\\u{9b}31m\n\t"
        );
    }

    #[test]
    fn paste_preserves_newlines_and_waits_for_enter() {
        let mut input = Input::default();
        assert!(input.edit(Event::Paste("first\nsecond".into())).is_none());
        assert_eq!(input.visible(30), "first↵second");
        assert_eq!(
            input.edit(Event::Key(KeyEvent::new(
                KeyCode::Enter,
                KeyModifiers::NONE
            ))),
            Some("first\nsecond".into())
        );
    }

    #[test]
    fn unicode_width_and_resize_preserve_input() {
        let mut input = Input("ab界🙂e\u{301}".into());
        let original = input.0.clone();
        for width in [0, 1, 2, 4, 10] {
            assert!(input.edit(Event::Resize(width, 20)).is_none());
            assert!(input.visible(width).width() <= usize::from(width));
            assert_eq!(input.0, original);
        }
        assert_eq!(input.visible(5), "界🙂e\u{301}");
    }

    fn text(spans: &[Span]) -> String {
        spans.iter().map(|span| span.text.as_str()).collect()
    }

    fn message(kind: Kind, chunks: &[&str], columns: u16) -> Vec<Span> {
        let mut output = Output::default();
        let mut out = Vec::new();
        for chunk in chunks {
            output.chunk(kind, chunk, columns, &mut out);
        }
        out.extend(output.finish(columns));
        out
    }

    #[test]
    fn messages_are_trimmed_and_wrapped_regardless_of_chunk_boundaries() {
        use Kind::*;
        for (case, kind, chunks, columns, expected) in [
            (
                "surrounding whitespace",
                Response,
                &["  \n\t hello world \n\n "][..],
                40,
                "\nhello world\n",
            ),
            ("whitespace only", Response, &[" \n\t ", "  "], 40, ""),
            (
                "interior blank lines",
                Response,
                &["one\n\n\ntwo\n"],
                40,
                "\none\n\n\ntwo\n",
            ),
            (
                "split words and spaces",
                Response,
                &["hel", "lo  ", " wor", "ld "],
                40,
                "\nhello   world\n",
            ),
            (
                "explicit newlines",
                Response,
                &["a\nb  \n c"],
                40,
                "\na\nb\n c\n",
            ),
            (
                "tabs",
                Response,
                &["a\tb\t\tc"],
                40,
                "\na       b               c\n",
            ),
            (
                "word wrap",
                Response,
                &["one two three four"],
                10,
                "\none two\nthree\nfour\n",
            ),
            (
                "long word",
                Response,
                &["ab abcdefgh"],
                6,
                "\nab\nabcde\nfgh\n",
            ),
            (
                "display width",
                Response,
                &["界界 界界界 界界界界"],
                7,
                "\n界界\n界界界\n界界界\n界\n",
            ),
            (
                "escaped control characters",
                Response,
                &["a\rb \x1b"],
                20,
                "\na\\rb \\u{1b}\n",
            ),
            (
                "reasoning",
                Reasoning,
                &["  weighing ", " it  "],
                40,
                "\nweighing  it\n",
            ),
            (
                "user prefix",
                User,
                &["  first line of text\nsecond  "],
                12,
                "> first\n  line of\n  text\n  second\n",
            ),
        ] {
            let joined = chunks.concat();
            let characters: Vec<String> = joined.chars().map(String::from).collect();
            let characters: Vec<&str> = characters.iter().map(String::as_str).collect();
            for chunks in [chunks, &[joined.as_str()], &characters] {
                let spans = message(kind, chunks, columns);
                assert_eq!(text(&spans), expected, "{case}: {chunks:?}");
                for span in spans.iter().filter(|span| !span.text.trim().is_empty()) {
                    assert_eq!(span.reasoning, kind == Reasoning, "{case}");
                }
            }
        }
    }

    #[test]
    fn model_tool_calls_show_one_clipped_line_of_name_and_compact_json() {
        let call = |name: Option<&str>, input: Option<Value>| {
            let mut call = ToolCall::new("one", "Search files").name(name.map(str::to_owned));
            call.raw_input = input;
            call
        };
        let json = |text: &str| Some(serde_json::from_str::<Value>(text).unwrap());
        let answer = ToolCall::new("two", "Final answer from subagent child")
            .status(ToolCallStatus::Completed)
            .content(vec![ToolCallContent::from(ContentBlock::from("Fixed.\n"))]);
        let message = r#"{"message":"Check the default server too","subagent_id":"child-1"}"#;
        for (case, call, columns, expected) in [
            (
                "compact JSON",
                call(Some("read_file"), json("{ \"path\" : \"src/config.rs\" }")),
                80,
                "read_file        {\"path\":\"src/config.rs\"}",
            ),
            (
                "key order as received",
                call(Some("grep"), json(r#"{"pattern":"X","path":"crates/ox"}"#)),
                80,
                r#"grep             {"pattern":"X","path":"crates/ox"}"#,
            ),
            (
                "escaped newlines",
                call(Some("apply_patch"), json(r#"{"patch":"a\nb"}"#)),
                80,
                r#"apply_patch      {"patch":"a\nb"}"#,
            ),
            (
                "malformed arguments",
                call(Some("shell"), Some(Value::String("{\"comm".into()))),
                80,
                r#"shell            "{\"comm""#,
            ),
            (
                "truncated",
                call(Some("send_message"), json(message)),
                30,
                r#"send_message     {"message..."#,
            ),
            (
                "very narrow",
                call(Some("send_message"), json(message)),
                3,
                "..",
            ),
            (
                "no name",
                call(None, json("{}"))
                    .content(vec![ToolCallContent::from(ContentBlock::from("output"))]),
                80,
                "Search files",
            ),
            ("no arguments", call(Some("glob"), None), 80, "Search files"),
            (
                "subagent answer",
                answer,
                80,
                "Final answer from subagent child\nFixed.",
            ),
        ] {
            let mut output = Output::default();
            let spans = output.update(SessionUpdate::ToolCall(call), columns);
            assert_eq!(text(&spans), format!("\n{expected}\n"), "{case}");
        }
    }

    #[test]
    fn partial_tool_updates_retain_omitted_fields_without_new_lines() {
        let mut output = Output::default();
        output.update(
            SessionUpdate::ToolCall(
                ToolCall::new("one", "Count")
                    .name("glob".to_owned())
                    .raw_input(serde_json::json!({"pattern": "*"}))
                    .content(vec![ToolCallContent::from(ContentBlock::from("details"))]),
            ),
            80,
        );
        for fields in [
            ToolCallUpdateFields::new().status(ToolCallStatus::Completed),
            ToolCallUpdateFields::new().raw_output(serde_json::json!("a\nb")),
        ] {
            let update = SessionUpdate::ToolCallUpdate(ToolCallUpdate::new("one", fields));
            assert!(output.update(update, 80).is_empty());
        }
        let tool = &output.tools[&ToolCallId::from("one")].call;
        assert_eq!(tool.status, ToolCallStatus::Completed);
        assert_eq!(tool.name.as_deref(), Some("glob"));
        assert_eq!(tool.content.len(), 1);
    }

    #[test]
    fn pending_words_are_written_before_tool_calls_permissions_and_other_output() {
        let tool = |id: &'static str| {
            SessionUpdate::ToolCall(
                ToolCall::new(id, "count")
                    .name("glob".to_owned())
                    .raw_input(serde_json::json!({"pattern": "*"}))
                    .content(vec![ToolCallContent::from(ContentBlock::from(
                        "every file",
                    ))]),
            )
        };
        let chunk = |text: &str| SessionUpdate::AgentMessageChunk(ContentChunk::new(text.into()));
        let request = RequestPermissionRequest::new(
            "session",
            ToolCallUpdate::new("two", ToolCallUpdateFields::new()),
            vec![PermissionOption::new(
                "go",
                "Go",
                PermissionOptionKind::AllowOnce,
            )],
        );
        let mut output = Output::default();
        let mut spans = output.user("  check  ", 40);
        spans.extend(output.update(chunk("Checking it  "), 40));
        spans.extend(output.update(tool("one"), 40));
        spans.extend(output.update(tool("two"), 40));
        spans.extend(output.update(chunk("Found it"), 40));
        spans.extend(output.permission(&request, 40));
        spans.extend(output.update(chunk("done  "), 40));
        spans.extend(output.finish(40));
        assert_eq!(
            text(&spans),
            "> check\n\nChecking it\n\nglob             {\"pattern\":\"*\"}\nglob             {\"pattern\":\"*\"}\n\nFound it\n\ncount\nevery file\nPermission required\n1. Go\n\ndone\n"
        );
    }
}
