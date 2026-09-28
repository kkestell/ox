mod input;
mod transcript;

use std::io::{Stdout, Write, stdout};
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use agent_client_protocol::schema::v1::*;
use chrono::DateTime;
use crossterm::{
    cursor::Show,
    event::{
        DisableBracketedPaste, DisableFocusChange, EnableBracketedPaste, EnableFocusChange, Event,
        EventStream, KeyCode, KeyEvent, KeyEventKind, KeyModifiers, KeyboardEnhancementFlags,
        PopKeyboardEnhancementFlags, PushKeyboardEnhancementFlags,
    },
    execute,
    terminal::{self, EnterAlternateScreen, LeaveAlternateScreen},
};
use futures::StreamExt;
use ratatui::{
    Frame,
    backend::CrosstermBackend,
    buffer::Buffer,
    layout::Rect,
    style::{Color, Style},
    text::{Line, Span},
};
use tokio::sync::mpsc::UnboundedReceiver;
use unicode_width::UnicodeWidthStr;

use crate::acp::{self, Session};
use input::Input;
use transcript::TranscriptView;

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

/// Whether the keyboard enhancement flags were pushed, so restoring pops them.
static ENHANCED: AtomicBool = AtomicBool::new(false);

fn restore() {
    // An empty title lets the tmux pane border show the pane's command again.
    let _ = write!(stdout(), "\x1b]2;\x1b\\");
    if ENHANCED.load(Ordering::SeqCst) {
        let _ = execute!(stdout(), PopKeyboardEnhancementFlags);
    }
    let _ = execute!(
        stdout(),
        DisableFocusChange,
        DisableBracketedPaste,
        LeaveAlternateScreen,
        Show
    );
    let _ = terminal::disable_raw_mode();
}

struct Terminal {
    inner: ratatui::Terminal<CrosstermBackend<Stdout>>,
    // Stays true in terminals that never report focus, so they get no bells or unseen results.
    focused: bool,
    title: String,
    /// The result of the last turn when it ended while the terminal was unfocused.
    unseen: Option<&'static str>,
}

impl Terminal {
    fn enter() -> anyhow::Result<Self> {
        terminal::enable_raw_mode()?;
        let inner = match ratatui::Terminal::new(CrosstermBackend::new(stdout())) {
            Ok(inner) => inner,
            Err(error) => {
                restore();
                return Err(error.into());
            }
        };
        let terminal = Self {
            inner,
            focused: true,
            title: String::new(),
            unseen: None,
        };
        // Queried before the event stream exists, since the answer arrives on stdin.
        let enhanced = terminal::supports_keyboard_enhancement().unwrap_or(false);
        execute!(
            stdout(),
            EnterAlternateScreen,
            EnableBracketedPaste,
            EnableFocusChange
        )?;
        if enhanced {
            execute!(
                stdout(),
                PushKeyboardEnhancementFlags(KeyboardEnhancementFlags::DISAMBIGUATE_ESCAPE_CODES)
            )?;
            ENHANCED.store(true, Ordering::SeqCst);
        }
        Ok(terminal)
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
}

impl Drop for Terminal {
    fn drop(&mut self) {
        restore();
    }
}

/// The transcript view's height and line count at the last draw, which paging
/// uses.
#[derive(Clone, Copy, Default)]
pub struct Layout {
    pub height: usize,
    pub lines: usize,
}

#[derive(Default)]
struct Ui {
    view: TranscriptView,
    input: Input,
    selected: usize,
    show_thinking: bool,
    layout: Layout,
    session_picker: Option<SessionPicker>,
    resume_after_turn: bool,
}

struct SessionPicker {
    sessions: Vec<SessionInfo>,
    selected: usize,
    first: usize,
    error: Option<String>,
}

impl SessionPicker {
    fn move_to(&mut self, selected: usize, height: usize) {
        self.selected = selected.min(self.sessions.len().saturating_sub(1));
        let rows = height.saturating_sub(3).max(1);
        if self.selected < self.first {
            self.first = self.selected;
        }
        if self.selected >= self.first + rows {
            self.first = self.selected + 1 - rows;
        }
    }
}

fn activity(session: &SessionInfo) -> Option<DateTime<chrono::FixedOffset>> {
    session
        .updated_at
        .as_deref()
        .and_then(|date| DateTime::parse_from_rfc3339(date).ok())
}

fn sorted_sessions(mut sessions: Vec<SessionInfo>) -> Vec<SessionInfo> {
    sessions.sort_by_key(|session| std::cmp::Reverse(activity(session)));
    sessions
}

pub struct Approval<'a> {
    pub request: &'a RequestPermissionRequest,
    pub selected: usize,
}

pub struct Screen<'a> {
    pub view: &'a TranscriptView,
    pub input: &'a Input,
    pub approval: Option<Approval<'a>>,
    session_picker: Option<&'a SessionPicker>,
    pub settings: &'a str,
    pub usage: &'a str,
    pub show_thinking: bool,
    pub now: Instant,
}

fn rule(width: usize) -> Line<'static> {
    Line::raw("─".repeat(width))
}

fn put(buf: &mut Buffer, area: Rect, y: usize, line: &Line) {
    if let Ok(y) = u16::try_from(y)
        && y < area.height
    {
        buf.set_line(area.x, area.y + y, line, area.width);
    }
}

pub fn draw(frame: &mut Frame, screen: &Screen) -> Layout {
    let area = frame.area();
    let width = usize::from(area.width);
    let rows = screen.input.rows(area.width);
    let approval = screen
        .session_picker
        .is_none()
        .then_some(())
        .and(
            screen
                .approval
                .as_ref()
                .map(|approval| approval_lines(screen.view, approval, width)),
        )
        .unwrap_or_default();
    let composer = rows.lines.len() + 3;
    let height = usize::from(area.height).saturating_sub(approval.len() + composer);
    let lines = if let Some(picker) = screen.session_picker {
        picker_lines(picker, width, height)
    } else {
        screen.view.lines(width, screen.show_thinking, screen.now)
    };
    let first = if screen.session_picker.is_some() {
        0
    } else {
        screen.view.first_row(height, lines.len())
    };
    let buf = frame.buffer_mut();
    for (y, line) in lines.iter().skip(first).take(height).enumerate() {
        put(buf, area, y, line);
    }
    if screen.session_picker.is_none() && screen.view.new_activity && height > 0 {
        let notice = "new activity";
        let padding = " ".repeat(width.saturating_sub(notice.width()) / 2);
        put(buf, area, height - 1, &Line::raw(" ".repeat(width)));
        put(
            buf,
            area,
            height - 1,
            &Line::styled(
                format!("{padding}{notice}"),
                Style::new().fg(Color::LightYellow),
            ),
        );
    }
    let mut y = height;
    for line in &approval {
        put(buf, area, y, line);
        y += 1;
    }
    put(buf, area, y, &rule(width));
    y += 1;
    let input_top = y;
    for line in &rows.lines {
        put(buf, area, y, &Line::raw(line.as_str()));
        y += 1;
    }
    put(buf, area, y, &rule(width));
    y += 1;
    let right = width.saturating_sub(screen.usage.width());
    put(
        buf,
        area,
        y,
        &Line::raw(transcript::clip(screen.settings, right.saturating_sub(1))),
    );
    if let Ok(y) = u16::try_from(y)
        && y < area.height
    {
        buf.set_string(
            area.x + right as u16,
            area.y + y,
            screen.usage,
            Style::new(),
        );
    }
    let (row, column) = rows.cursor;
    let x = u16::try_from(column)
        .unwrap_or(u16::MAX)
        .min(area.width.saturating_sub(1));
    let y = u16::try_from(input_top + row)
        .unwrap_or(u16::MAX)
        .min(area.height.saturating_sub(1));
    frame.set_cursor_position((area.x + x, area.y + y));
    Layout {
        height,
        lines: lines.len(),
    }
}

fn picker_lines(picker: &SessionPicker, width: usize, height: usize) -> Vec<Line<'static>> {
    let mut lines = vec![Line::raw(transcript::clip(
        "Resume a session  ↑/↓ move  Enter load  Esc cancel",
        width,
    ))];
    if let Some(error) = &picker.error {
        lines.push(Line::styled(
            transcript::clip(error, width),
            Style::new().fg(Color::Red),
        ));
    } else {
        lines.push(Line::default());
    }
    if picker.sessions.is_empty() {
        lines.push(Line::raw("No saved sessions"));
        return lines;
    }
    for (index, session) in picker
        .sessions
        .iter()
        .enumerate()
        .skip(picker.first)
        .take(height.saturating_sub(3))
    {
        let date = activity(session).map_or_else(
            || "Unknown date".to_owned(),
            |date| date.format("%Y-%m-%d").to_string(),
        );
        let marker = if index == picker.selected { "›" } else { " " };
        let title = escape(session.title.as_deref().unwrap_or("Untitled session"));
        let title_width = width.saturating_sub(date.width() + 3);
        let title = transcript::clip(&title, title_width);
        let padding = " ".repeat(width.saturating_sub(2 + title.width() + date.width()));
        lines.push(Line::raw(format!("{marker} {title}{padding}{date}")));
    }
    lines
}

fn approval_lines(view: &TranscriptView, approval: &Approval, width: usize) -> Vec<Line<'static>> {
    let request = approval.request;
    let id = &request.tool_call.tool_call_id;
    let mut call = view
        .tool(id)
        .cloned()
        .unwrap_or_else(|| ToolCall::new(id.clone(), ""));
    call.update(request.tool_call.fields.clone());
    // The dialog pads its content by two columns on each side.
    let content_width = width.saturating_sub(4);
    // Shell content names the subagent, if any, and everything being approved.
    let (heading, body) = match call.name.as_deref() {
        Some("shell") => (
            "Would you like to run the following command?",
            transcript::content_lines(&call, content_width),
        ),
        Some("shell_process") => (
            "Would you like to send the following input?",
            transcript::content_lines(&call, content_width),
        ),
        _ => (
            "Would you like to allow the following?",
            transcript::described_lines(&call, content_width),
        ),
    };
    let mut lines = vec![Line::raw(heading), Line::default()];
    lines.extend(body);
    lines.push(Line::default());
    for (index, option) in request.options.iter().enumerate() {
        let marker = if index == approval.selected {
            "›"
        } else {
            " "
        };
        lines.push(Line::raw(format!(
            "{marker} {}. {}",
            index + 1,
            option.name
        )));
    }
    for line in &mut lines {
        line.spans.insert(0, Span::raw("  "));
    }
    lines.splice(0..0, [rule(width), Line::default()]);
    lines.push(Line::default());
    lines
}

/// The status line's left side: the mode, model, and effort in use.
pub fn settings(options: &[SessionConfigOption]) -> String {
    use SessionConfigOptionCategory::*;
    [Mode, Model, ThoughtLevel]
        .iter()
        .filter_map(|category| {
            let option = options
                .iter()
                .find(|option| option.category.as_ref() == Some(category))?;
            match &option.kind {
                SessionConfigKind::Select(select) => Some(select.current_value.to_string()),
                _ => None,
            }
        })
        .collect::<Vec<_>>()
        .join(" • ")
}

/// The status line's right side: the context used and the session cost.
pub fn usage(usage: Option<&UsageUpdate>) -> String {
    let (percent, cost) = match usage {
        Some(usage) => {
            let percent = if usage.size == 0 {
                0.0
            } else {
                (usage.used as f64 * 100.0 / usage.size as f64).round()
            };
            (percent, usage.cost.as_ref().map_or(0.0, |cost| cost.amount))
        }
        None => (0.0, 0.0),
    };
    format!("{percent}% • ${cost:.2}")
}

/// Handles one key and reports whether to quit.
async fn key(
    ui: &mut Ui,
    session: &mut Session,
    key: KeyEvent,
    now: Instant,
) -> anyhow::Result<bool> {
    if key.kind == KeyEventKind::Release {
        return Ok(false);
    }
    if let Some(picker) = &mut ui.session_picker {
        let page = ui.layout.height.saturating_sub(3).max(1);
        match key.code {
            KeyCode::Up => picker.move_to(picker.selected.saturating_sub(1), ui.layout.height),
            KeyCode::Down => picker.move_to(picker.selected.saturating_add(1), ui.layout.height),
            KeyCode::PageUp => {
                picker.move_to(picker.selected.saturating_sub(page), ui.layout.height)
            }
            KeyCode::PageDown => {
                picker.move_to(picker.selected.saturating_add(page), ui.layout.height)
            }
            KeyCode::Home => picker.move_to(0, ui.layout.height),
            KeyCode::End => {
                picker.move_to(picker.sessions.len().saturating_sub(1), ui.layout.height)
            }
            KeyCode::Esc if session.active() => ui.session_picker = None,
            KeyCode::Enter if !picker.sessions.is_empty() => {
                let id = picker.sessions[picker.selected].session_id.clone();
                if session.active()
                    && let Err(error) = session.close().await
                {
                    picker.error = Some(format!("Close failed: {error}"));
                    return Ok(false);
                }
                ui.view = TranscriptView::default();
                match session.load(id).await {
                    Ok(()) => {
                        ui.session_picker = None;
                        ui.input.clear();
                    }
                    Err(error) => picker.error = Some(format!("Load failed: {error}")),
                }
            }
            KeyCode::Char('c' | 'd') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                return Ok(true);
            }
            _ => {}
        }
        return Ok(false);
    }
    let control = key.modifiers.contains(KeyModifiers::CONTROL);
    let options = session
        .pending
        .front()
        .map(|(request, _)| request.options.len());
    match key.code {
        KeyCode::Tab | KeyCode::BackTab
            if !key
                .modifiers
                .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT) =>
        {
            let forward = key.code == KeyCode::Tab && !key.modifiers.contains(KeyModifiers::SHIFT);
            if let Some((id, value)) = next_mode(&session.config_options, forward)
                && let Err(error) = session.set_config_option(id, value).await
            {
                ui.view
                    .notice(format!("Mode change failed: {error}"), Color::Red, now);
            }
        }
        KeyCode::Char('c' | 'd') if control => {
            session.cancel()?;
            return Ok(true);
        }
        KeyCode::Char('t') if control => ui.show_thinking = !ui.show_thinking,
        KeyCode::Char('u') if control => ui.input.clear(),
        KeyCode::Char(c)
            if !key
                .modifiers
                .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT) =>
        {
            ui.input.insert(c);
        }
        KeyCode::Enter if key.modifiers.contains(KeyModifiers::SHIFT) => ui.input.newline(),
        KeyCode::Enter => {
            if ui.resume_after_turn {
                return Ok(false);
            }
            if !ui.input.is_empty() {
                if ui.input.text() == "/resume" {
                    ui.input.clear();
                    if !session.can_resume() {
                        ui.view
                            .notice("Session resume is unavailable".into(), Color::Red, now);
                    } else if session.busy {
                        session.queued = None;
                        ui.resume_after_turn = true;
                        session.cancel()?;
                    } else {
                        open_picker(ui, session, now).await;
                    }
                } else {
                    send(ui, session, now)?;
                }
            } else if options.is_some() {
                answer(ui, session, ui.selected)?;
            }
        }
        KeyCode::Esc => {
            if let Some((request, _)) = session.pending.front() {
                let reject = request
                    .options
                    .iter()
                    .position(|option| {
                        matches!(
                            option.kind,
                            PermissionOptionKind::RejectOnce | PermissionOptionKind::RejectAlways
                        )
                    })
                    .unwrap_or(request.options.len().saturating_sub(1));
                answer(ui, session, reject)?;
            } else if session.busy {
                session.cancel()?;
            }
        }
        KeyCode::Up => match options {
            Some(_) => ui.selected = ui.selected.saturating_sub(1),
            None => ui.input.up(),
        },
        KeyCode::Down => match options {
            Some(count) => ui.selected = (ui.selected + 1).min(count.saturating_sub(1)),
            None => ui.input.down(),
        },
        KeyCode::Left => ui.input.left(),
        KeyCode::Right => ui.input.right(),
        KeyCode::Home => ui.input.home(),
        KeyCode::End => {
            ui.input.end();
            ui.view.end();
        }
        KeyCode::Backspace => ui.input.backspace(),
        KeyCode::Delete => ui.input.delete(),
        KeyCode::PageUp => ui.view.page_up(ui.layout.height, ui.layout.lines),
        KeyCode::PageDown => ui.view.page_down(ui.layout.height, ui.layout.lines),
        _ => {}
    }
    Ok(false)
}

async fn open_picker(ui: &mut Ui, session: &mut Session, now: Instant) {
    match session.list().await {
        Ok(sessions) => {
            ui.session_picker = Some(SessionPicker {
                sessions: sorted_sessions(sessions),
                selected: 0,
                first: 0,
                error: None,
            })
        }
        Err(error) => ui
            .view
            .notice(format!("Session list failed: {error}"), Color::Red, now),
    }
}

fn next_mode(
    options: &[SessionConfigOption],
    forward: bool,
) -> Option<(SessionConfigId, SessionConfigValueId)> {
    let option = options
        .iter()
        .find(|option| option.category == Some(SessionConfigOptionCategory::Mode))?;
    let SessionConfigKind::Select(select) = &option.kind else {
        return None;
    };
    let choices: Vec<_> = match &select.options {
        SessionConfigSelectOptions::Ungrouped(choices) => choices.iter().collect(),
        SessionConfigSelectOptions::Grouped(groups) => groups
            .iter()
            .flat_map(|group| group.options.iter())
            .collect(),
        _ => return None,
    };
    let count = choices.len();
    if count < 2 {
        return None;
    }
    let current = choices
        .iter()
        .position(|choice| choice.value == select.current_value)?;
    let next = if forward {
        (current + 1) % count
    } else {
        (current + count - 1) % count
    };
    Some((option.id.clone(), choices[next].value.clone()))
}

/// Sends the input. During a turn the prompt is queued, and its `User` item
/// waits until the turn finishes.
fn send(ui: &mut Ui, session: &mut Session, now: Instant) -> anyhow::Result<()> {
    let text = ui.input.take();
    let busy = session.busy;
    session.prompt(text.clone())?;
    if !busy {
        ui.view.user(text, now);
    }
    Ok(())
}

fn answer(ui: &mut Ui, session: &mut Session, index: usize) -> anyhow::Result<()> {
    session.answer(index + 1)?;
    ui.selected = 0;
    if !session.pending.is_empty() {
        ui.view.changed();
    }
    Ok(())
}

fn handle(
    ui: &mut Ui,
    session: &mut Session,
    terminal: &mut Terminal,
    event: acp::Event,
    now: Instant,
) -> anyhow::Result<()> {
    match event {
        acp::Event::Update(id, update) => {
            if session.accepts(&id) {
                session.update(&update);
                ui.view.update(update, now);
            }
        }
        acp::Event::Diagnostic(text) => ui.view.notice(text, Color::DarkGray, now),
        acp::Event::Permission(id, request, responder) => {
            if !session.accepts(&id) {
                responder.respond(RequestPermissionResponse::new(
                    RequestPermissionOutcome::Cancelled,
                ))?;
                return Ok(());
            }
            let first = session.pending.is_empty();
            session.permission(request, responder)?;
            if first && !session.pending.is_empty() {
                ui.selected = 0;
                ui.view.changed();
                terminal.bell()?;
            }
        }
        acp::Event::Finished(id, result) => {
            if !session.accepts(&id) {
                return Ok(());
            }
            let queued = session.finished()?;
            ui.view.end_turn(now);
            terminal.unseen = (!terminal.focused).then_some(if result.is_err() {
                "turn error"
            } else {
                "finished"
            });
            terminal.bell()?;
            if let Err(error) = result {
                ui.view
                    .notice(format!("Turn error: {error}"), Color::Red, now);
            }
            if let Some(text) = queued {
                ui.view.user(text, now);
            }
        }
    }
    Ok(())
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
    let mut ui = Ui::default();
    let mut tick = tokio::time::interval(Duration::from_secs(1));
    loop {
        terminal.status(if ui.session_picker.is_some() {
            "resume session"
        } else if !session.pending.is_empty() {
            "needs permission"
        } else if session.busy {
            "working"
        } else {
            terminal.unseen.unwrap_or("ready")
        })?;
        let settings = settings(&session.config_options);
        let usage = usage(session.usage.as_ref());
        let screen = Screen {
            view: &ui.view,
            input: &ui.input,
            approval: session.pending.front().map(|(request, _)| Approval {
                request,
                selected: ui.selected,
            }),
            session_picker: ui.session_picker.as_ref(),
            settings: &settings,
            usage: &usage,
            show_thinking: ui.show_thinking,
            now: Instant::now(),
        };
        let mut layout = Layout::default();
        terminal.inner.draw(|frame| layout = draw(frame, &screen))?;
        ui.layout = layout;
        tokio::select! {
            biased;
            _ = session.closed() => anyhow::bail!("server closed the ACP connection"),
            Some(event) = events.recv() => {
                handle(&mut ui, &mut session, &mut terminal, event, Instant::now())?;
                while let Ok(event) = events.try_recv() {
                    handle(&mut ui, &mut session, &mut terminal, event, Instant::now())?;
                }
                if ui.resume_after_turn && !session.busy {
                    ui.resume_after_turn = false;
                    open_picker(&mut ui, &mut session, Instant::now()).await;
                }
            }
            _ = tick.tick() => {}
            event = keys.next() => {
                let Some(event) = event else { session.cancel()?; return Ok(()) };
                match event? {
                    Event::FocusGained => { terminal.focused = true; terminal.unseen = None; }
                    Event::FocusLost => terminal.focused = false,
                    Event::Paste(text) => ui.input.paste(&text),
                    Event::Key(event) => {
                        let quit = key(&mut ui, &mut session, event, Instant::now()).await?;
                        if quit {
                            return Ok(());
                        }
                    }
                    _ => {}
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::acp::Event as AcpEvent;
    use crate::acp::tests::{turn, with_session};
    use ratatui::backend::TestBackend;

    #[test]
    fn server_control_characters_are_printed_as_text() {
        assert_eq!(
            escape("\x1b[2J\r\x07\u{009b}31m\n\t"),
            "\\u{1b}[2J\\r\\u{7}\\u{9b}31m\n\t"
        );
    }

    /// Draws the screen and returns its rows, the cursor, and the layout.
    fn render(screen: &Screen, width: u16, height: u16) -> (Vec<String>, (u16, u16), Layout) {
        let mut terminal = ratatui::Terminal::new(TestBackend::new(width, height)).unwrap();
        let mut layout = Layout::default();
        terminal.draw(|frame| layout = draw(frame, screen)).unwrap();
        let cursor = terminal.get_cursor_position().unwrap();
        let buffer = terminal.backend().buffer();
        let rows = (0..height)
            .map(|y| {
                (0..width)
                    .map(|x| buffer.cell((x, y)).unwrap().symbol())
                    .collect::<String>()
                    .trim_end()
                    .to_owned()
            })
            .collect();
        (rows, (cursor.x, cursor.y), layout)
    }

    fn request(name: &str, content: &str) -> RequestPermissionRequest {
        RequestPermissionRequest::new(
            "session",
            ToolCallUpdate::new(
                "call-1",
                ToolCallUpdateFields::new()
                    .name(name.to_owned())
                    .status(ToolCallStatus::Pending)
                    .content(vec![ToolCallContent::from(ContentBlock::Text(
                        TextContent::new(content),
                    ))]),
            ),
            vec![
                PermissionOption::new("approve", "Yes", PermissionOptionKind::AllowOnce),
                PermissionOption::new("deny", "No", PermissionOptionKind::RejectOnce),
            ],
        )
    }

    fn screen<'a>(view: &'a TranscriptView, input: &'a Input, now: Instant) -> Screen<'a> {
        Screen {
            view,
            input,
            approval: None,
            session_picker: None,
            settings: "",
            usage: "0% • $0.00",
            show_thinking: false,
            now,
        }
    }

    #[test]
    fn the_frame_places_the_transcript_view_the_approval_block_and_the_composer() {
        let start = Instant::now();
        let now = start + Duration::from_secs(20);
        let mut view = TranscriptView::default();
        view.user("Run the tests!".to_owned(), start);
        view.update(
            SessionUpdate::AgentThoughtChunk(ContentChunk::new("weighing".into())),
            start,
        );
        for (id, title, name) in [
            ("read", "Read Makefile", "read_file"),
            ("glob", "Find files matching *.rs", "glob"),
        ] {
            let call = ToolCall::new(id, title)
                .name(name.to_owned())
                .status(ToolCallStatus::Completed);
            view.update(
                SessionUpdate::ToolCall(call),
                start + Duration::from_secs(12),
            );
        }
        let shell = ToolCall::new("shell-0", "ls -la")
            .name("shell".to_owned())
            .status(ToolCallStatus::Completed)
            .raw_input(serde_json::json!({"command": "ls -la"}));
        view.update(SessionUpdate::ToolCall(shell), now);
        view.update(
            SessionUpdate::AgentMessageChunk(ContentChunk::new(
                "Two tallies were counted in the workspace.".into(),
            )),
            now,
        );
        let mut input = Input::default();
        input.paste("Also check the docs\nwhen you are done");
        let request = request(
            "shell",
            "Working directory: /workspace\n\nCommand:\n\n    cargo test",
        );
        let screen = Screen {
            approval: Some(Approval {
                request: &request,
                selected: 0,
            }),
            settings: "ask • deepseek/deepseek-v4-flash • high",
            usage: "5% • $0.01",
            ..screen(&view, &input, now)
        };
        let (rows, cursor, layout) = render(&screen, 72, 26);
        let rule = "─".repeat(72);
        assert_eq!(
            rows,
            [
                "",
                "● Thought for 12s",
                "",
                "● Read Makefile",
                "● Find files matching *.rs",
                "● Shell ls -la",
                "",
                "● Two tallies were counted in the workspace.",
                &rule,
                "",
                "  Would you like to run the following command?",
                "",
                "    Working directory: /workspace",
                "",
                "    Command:",
                "",
                "        cargo test",
                "",
                "  › 1. Yes",
                "    2. No",
                "",
                &rule,
                "❯ Also check the docs",
                "  when you are done",
                &rule,
                &format!(
                    "{:<62}5% • $0.01",
                    "ask • deepseek/deepseek-v4-flash • high"
                ),
            ]
        );
        assert_eq!(cursor, (19, 23));
        assert_eq!((layout.height, layout.lines), (8, 9));
    }

    #[test]
    fn a_shell_process_approval_shows_its_content_without_the_tool_line() {
        let view = TranscriptView::default();
        let request = request("shell_process", "Shell process: p-1\n\nInput:\n\n    y");
        let lines = approval_lines(
            &view,
            &Approval {
                request: &request,
                selected: 0,
            },
            40,
        );
        let rows: Vec<String> = lines.iter().map(ToString::to_string).collect();
        assert_eq!(
            rows[2..],
            [
                "  Would you like to send the following input?",
                "  ",
                "    Shell process: p-1",
                "    ",
                "    Input:",
                "    ",
                "        y",
                "  ",
                "  › 1. Yes",
                "    2. No",
                "",
            ]
        );
    }

    #[test]
    fn the_new_activity_notice_covers_the_last_row_only_while_auto_scroll_is_off() {
        let now = Instant::now();
        let mut view = TranscriptView::default();
        for index in 0..20 {
            view.user(format!("message {index}"), now);
        }
        let input = Input::default();
        let (rows, _, layout) = render(&screen(&view, &input, now), 40, 10);
        assert_eq!((layout.height, layout.lines), (6, 39));
        assert_eq!(rows[5], "❯ message 19");
        view.page_up(layout.height, layout.lines);
        let (rows, _, _) = render(&screen(&view, &input, now), 40, 10);
        assert_eq!(rows[4..6], ["❯ message 16", ""]);
        view.user("message 20".to_owned(), now);
        let (rows, _, _) = render(&screen(&view, &input, now), 40, 10);
        assert_eq!(
            rows[..5],
            ["❯ message 14", "", "❯ message 15", "", "❯ message 16"]
        );
        assert_eq!(rows[5], "              new activity");
        let mut terminal = ratatui::Terminal::new(TestBackend::new(40, 10)).unwrap();
        terminal
            .draw(|frame| {
                draw(frame, &screen(&view, &input, now));
            })
            .unwrap();
        let cell = terminal.backend().buffer().cell((14, 5)).unwrap();
        assert_eq!(cell.fg, Color::LightYellow);
        view.end();
        let (rows, _, _) = render(&screen(&view, &input, now), 40, 10);
        assert_eq!(rows[5], "❯ message 20");
    }

    #[test]
    fn the_status_line_shows_settings_on_the_left_and_usage_on_the_right() {
        let select = |id: &str, value: &str, category: Option<SessionConfigOptionCategory>| {
            SessionConfigOption::select(
                id.to_owned(),
                id,
                value.to_owned(),
                vec![SessionConfigSelectOption::new(value.to_owned(), value)],
            )
            .category(category)
        };
        use SessionConfigOptionCategory::*;
        let options = vec![
            select("model", "deepseek", Some(Model)),
            select("effort", "high", Some(ThoughtLevel)),
            select("pace", "steady", None),
            select("approval", "ask", Some(Mode)),
        ];
        assert_eq!(settings(&options), "ask • deepseek • high");
        assert_eq!(settings(&options[..1]), "deepseek");
        assert_eq!(settings(&options[2..3]), "");
        assert_eq!(usage(None), "0% • $0.00");
        let update = UsageUpdate::new(1200, 8000).cost(Cost::new(0.25, "USD"));
        assert_eq!(usage(Some(&update)), "15% • $0.25");
        assert_eq!(usage(Some(&UsageUpdate::new(2, 3))), "67% • $0.00");
        let view = TranscriptView::default();
        let input = Input::default();
        let screen = Screen {
            settings: "ask • deepseek • high",
            usage: "15% • $0.25",
            ..screen(&view, &input, Instant::now())
        };
        let (rows, cursor, _) = render(&screen, 40, 6);
        assert_eq!(
            rows[5],
            format!("{:<29}15% • $0.25", "ask • deepseek • high")
        );
        assert_eq!(rows[3], "❯");
        assert_eq!(cursor, (2, 3));
    }

    #[test]
    fn session_picker_sorts_dates_and_keeps_selection_visible() {
        let mut picker = SessionPicker {
            sessions: sorted_sessions(vec![
                SessionInfo::new("a", "/tmp")
                    .title("older")
                    .updated_at("2026-09-28T00:30:00+05:00"),
                SessionInfo::new("b", "/tmp")
                    .title("newer")
                    .updated_at("2026-09-27T23:00:00Z"),
                SessionInfo::new("c", "/tmp").title("undated"),
            ]),
            selected: 0,
            first: 0,
            error: None,
        };
        assert_eq!(
            picker
                .sessions
                .iter()
                .map(|item| item.session_id.to_string())
                .collect::<Vec<_>>(),
            ["b", "a", "c"]
        );
        let view = TranscriptView::default();
        let input = Input::default();
        let layout = {
            let mut display = screen(&view, &input, Instant::now());
            display.session_picker = Some(&picker);
            let (rows, _, layout) = render(&display, 40, 8);
            assert!(rows[2].contains("newer"));
            assert!(rows[2].contains("2026-09-27"));
            layout
        };
        picker.move_to(2, layout.height);
        let mut screen = screen(&view, &input, Instant::now());
        screen.session_picker = Some(&picker);
        let (rows, _, _) = render(&screen, 40, 8);
        assert!(
            rows.iter()
                .any(|row| row.contains("› undated") && row.contains("Unknown date"))
        );
    }

    #[tokio::test]
    async fn resume_keys_cancel_or_reload_the_current_session() {
        with_session(async |mut session, _events| {
            let old = session.id().clone();
            let mut ui = Ui::default();
            let now = Instant::now();
            ui.input.paste("/resume");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.session_picker.is_some());
            press(&mut ui, &mut session, KeyCode::Esc, now).await?;
            assert!(ui.session_picker.is_none());
            assert_eq!(session.id(), &old);
            ui.input.paste("/resume");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.session_picker.is_none());
            assert_eq!(session.id(), &old);
            assert!(session.active());
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn resume_wait_does_not_queue_another_prompt() {
        with_session(async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            session.prompt("running".into())?;
            ui.input.paste("/resume");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.resume_after_turn);
            ui.input.paste("later");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert_eq!(ui.input.text(), "later");
            assert!(session.queued.is_none());
            Ok(())
        })
        .await;
    }

    /// Collects the `tools` script's two permission requests.
    async fn requests(session: &mut Session, events: &mut UnboundedReceiver<AcpEvent>) {
        while session.pending.len() < 2 {
            if let AcpEvent::Permission(_, request, responder) = events.recv().await.unwrap() {
                session.permission(request, responder).unwrap();
            }
        }
    }

    async fn press(
        ui: &mut Ui,
        session: &mut Session,
        code: KeyCode,
        now: Instant,
    ) -> anyhow::Result<bool> {
        key(ui, session, KeyEvent::new(code, KeyModifiers::NONE), now).await
    }

    #[tokio::test]
    async fn tab_and_backtab_cycle_the_available_modes() {
        with_session(async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            assert_eq!(settings(&session.config_options), "ask");
            for (code, expected) in [
                (KeyCode::Tab, "auto"),
                (KeyCode::Tab, "ask"),
                (KeyCode::BackTab, "auto"),
                (KeyCode::BackTab, "ask"),
            ] {
                key(
                    &mut ui,
                    &mut session,
                    KeyEvent::new(code, KeyModifiers::NONE),
                    now,
                )
                .await?;
                assert_eq!(settings(&session.config_options), expected);
            }
            assert!(ui.input.is_empty());
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn approval_keys_move_the_selection_and_enter_answers_only_with_an_empty_input() {
        with_session(async |mut session, mut events| {
            let now = Instant::now();
            let mut ui = Ui::default();
            session.prompt("tools".into())?;
            requests(&mut session, &mut events).await;
            press(&mut ui, &mut session, KeyCode::Down, now).await?;
            press(&mut ui, &mut session, KeyCode::Down, now).await?;
            assert_eq!(ui.selected, 1);
            press(&mut ui, &mut session, KeyCode::Esc, now).await?;
            assert_eq!((session.pending.len(), ui.selected), (1, 0));
            press(&mut ui, &mut session, KeyCode::Up, now).await?;
            press(&mut ui, &mut session, KeyCode::Down, now).await?;
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(session.pending.is_empty());
            let (text, ok) = turn(&mut session, &mut events, &[]).await;
            assert!(ok);
            assert_eq!(text, "tally-1: stop, tally-2: stop");
            session.prompt("tools".into())?;
            requests(&mut session, &mut events).await;
            press(&mut ui, &mut session, KeyCode::Char('h'), now).await?;
            press(&mut ui, &mut session, KeyCode::Char('i'), now).await?;
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(session.pending.is_empty());
            assert_eq!(session.queued.as_deref(), Some("hi"));
            assert!(ui.input.is_empty());
            let queued = loop {
                match events.recv().await.unwrap() {
                    AcpEvent::Permission(_, request, responder) => {
                        session.permission(request, responder)?;
                    }
                    AcpEvent::Finished(..) => break session.finished()?,
                    _ => {}
                }
            };
            assert_eq!(queued.as_deref(), Some("hi"));
            assert_eq!(
                turn(&mut session, &mut events, &[]).await,
                ("you said: hi".into(), true)
            );
            Ok(())
        })
        .await;
    }
}
