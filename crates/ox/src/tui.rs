use std::collections::HashMap;
use std::io::{Write, stdout};

use agent_client_protocol::schema::v1::*;
use crossterm::{
    cursor::{Hide, MoveToColumn, Show},
    event::{
        DisableBracketedPaste, DisableFocusChange, EnableBracketedPaste, EnableFocusChange, Event,
        EventStream, KeyCode, KeyEventKind, KeyModifiers,
    },
    execute,
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
    let _ = execute!(stdout(), DisableFocusChange, DisableBracketedPaste, Show);
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

    fn print(&mut self, text: &str) -> anyhow::Result<()> {
        self.erase()?;
        if !text.is_empty() {
            write!(stdout(), "{}", escape(text).replace('\n', "\r\n"))?;
            self.line_start = text.ends_with('\n');
        }
        stdout().flush()?;
        Ok(())
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

#[derive(Default)]
struct Output {
    tools: HashMap<ToolCallId, ToolCall>,
    thinking: bool,
}

impl Output {
    fn tool_update(&mut self, update: ToolCallUpdate) -> String {
        let show_content = update.fields.content.is_some();
        let tool = self
            .tools
            .entry(update.tool_call_id.clone())
            .or_insert_with(|| ToolCall::new(update.tool_call_id, "Tool"));
        tool.update(update.fields);
        tool_text(tool, show_content)
    }

    fn update(&mut self, update: SessionUpdate) -> String {
        match update {
            SessionUpdate::AgentMessageChunk(chunk) => {
                let prefix = if self.thinking { "\n" } else { "" };
                self.thinking = false;
                format!("{prefix}{}", content(&chunk.content))
            }
            SessionUpdate::AgentThoughtChunk(chunk) => {
                let prefix = if self.thinking { "" } else { "\nThinking\n" };
                self.thinking = true;
                format!("{prefix}{}", content(&chunk.content))
            }
            SessionUpdate::ToolCall(tool) => {
                self.thinking = false;
                let text = tool_text(&tool, true);
                self.tools.insert(tool.tool_call_id.clone(), tool);
                text
            }
            SessionUpdate::ToolCallUpdate(update) => self.tool_update(update),
            _ => String::new(),
        }
    }

    fn permission(&mut self, request: &RequestPermissionRequest) -> String {
        let mut text = self.tool_update(request.tool_call.clone());
        if let Some(tool) = self.tools.get(&request.tool_call.tool_call_id)
            && request.tool_call.fields.content.is_none()
        {
            text.push_str(&tool_content(&tool.content));
        }
        text.push_str("Permission required\n");
        for (index, option) in request.options.iter().enumerate() {
            text.push_str(&format!("{}. {}\n", index + 1, option.name));
        }
        text
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

fn tool_text(tool: &ToolCall, show_content: bool) -> String {
    let status = serde_json::to_value(tool.status).expect("tool status is serializable");
    let mut text = format!(
        "\n{} [{}]\n",
        tool.title,
        status.as_str().expect("tool status is a string")
    );
    if show_content {
        text.push_str(&tool_content(&tool.content));
    }
    text
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
                acp::Event::Update(update) => terminal.print(&output.update(update))?,
                acp::Event::Diagnostic(text) => { terminal.newline()?; terminal.print(&format!("{text}\n"))?; }
                acp::Event::Permission(request, responder) => {
                    let first = session.pending.is_empty();
                    session.permission(request, responder)?;
                    if first && let Some((request, _)) = session.pending.front() {
                        input.0.clear();
                        terminal.print(&output.permission(request))?;
                        terminal.bell()?;
                    }
                }
                acp::Event::Finished(result) => {
                    session.finished()?;
                    input.0.clear();
                    output.thinking = false;
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
                                if session.busy { session.cancel()?; input.0.clear(); terminal.print("\nCancelling…\n")?; }
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
                                terminal.print(&format!("> {text}\n"))?;
                                session.prompt(text)?;
                            }
                        } else if !session.answer(text.trim().parse().unwrap_or(0))? {
                            terminal.print("Enter one of the supplied option numbers\n")?;
                        } else if let Some((request, _)) = session.pending.front() {
                            terminal.print(&output.permission(request))?;
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

    #[test]
    fn partial_tool_updates_retain_omitted_fields() {
        let mut output = Output::default();
        output.update(SessionUpdate::ToolCall(
            ToolCall::new("one", "Count")
                .content(vec![ToolCallContent::from(ContentBlock::from("details"))]),
        ));
        let text = output.tool_update(ToolCallUpdate::new(
            "one",
            ToolCallUpdateFields::new().status(ToolCallStatus::Completed),
        ));
        assert!(text.contains("Count [completed]"));
        let tool = &output.tools[&ToolCallId::from("one")];
        assert_eq!(tool.content.len(), 1);
        assert!(!text.contains("details"));
    }
}
