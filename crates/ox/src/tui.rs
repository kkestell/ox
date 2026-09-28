mod input;
mod transcript;

use std::io::{Stdout, Write, stdout};
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use agent_client_protocol::schema::v1::*;
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
    text::Line,
};
use serde_json::Value;
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
}

pub struct Approval<'a> {
    pub request: &'a RequestPermissionRequest,
    pub selected: usize,
}

pub struct Screen<'a> {
    pub view: &'a TranscriptView,
    pub input: &'a Input,
    pub approval: Option<Approval<'a>>,
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
        .approval
        .as_ref()
        .map(|approval| approval_lines(screen.view, approval, width))
        .unwrap_or_default();
    let composer = rows.lines.len() + 3;
    let height = usize::from(area.height).saturating_sub(approval.len() + composer);
    let lines = screen.view.lines(width, screen.show_thinking, screen.now);
    let first = screen.view.first_row(height, lines.len());
    let buf = frame.buffer_mut();
    for (y, line) in lines.iter().skip(first).take(height).enumerate() {
        put(buf, area, y, line);
    }
    if screen.view.new_activity && height > 0 {
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

fn approval_lines(view: &TranscriptView, approval: &Approval, width: usize) -> Vec<Line<'static>> {
    let request = approval.request;
    let id = &request.tool_call.tool_call_id;
    let mut call = view
        .tool(id)
        .cloned()
        .unwrap_or_else(|| ToolCall::new(id.clone(), ""));
    call.update(request.tool_call.fields.clone());
    let subagent = request
        .tool_call
        .meta
        .as_ref()
        .and_then(|meta| meta.get("subagent_id"))
        .and_then(Value::as_str);
    let heading = if call.name.as_deref() == Some("shell") {
        match subagent {
            Some(subagent) => {
                format!("Subagent {subagent} would like to run the following command.")
            }
            None => "Would you like to run the following command?".to_owned(),
        }
    } else {
        "Would you like to allow the following?".to_owned()
    };
    let mut lines = vec![
        rule(width),
        Line::default(),
        Line::raw(heading),
        Line::default(),
    ];
    lines.extend(transcript::call_lines(&call, width));
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
fn key(ui: &mut Ui, session: &mut Session, key: KeyEvent, now: Instant) -> anyhow::Result<bool> {
    if key.kind == KeyEventKind::Release {
        return Ok(false);
    }
    let control = key.modifiers.contains(KeyModifiers::CONTROL);
    let options = session
        .pending
        .front()
        .map(|(request, _)| request.options.len());
    match key.code {
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
            if !ui.input.is_empty() {
                send(ui, session, now)?;
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
        acp::Event::Update(update) => {
            session.update(&update);
            ui.view.update(update, now);
        }
        acp::Event::Diagnostic(text) => ui.view.notice(text, Color::DarkGray, now),
        acp::Event::Permission(request, responder) => {
            let first = session.pending.is_empty();
            session.permission(request, responder)?;
            if first && !session.pending.is_empty() {
                ui.selected = 0;
                ui.view.changed();
                terminal.bell()?;
            }
        }
        acp::Event::Finished(result) => {
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
        terminal.status(if !session.pending.is_empty() {
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
            }
            _ = tick.tick() => {}
            event = keys.next() => {
                let Some(event) = event else { session.cancel()?; return Ok(()) };
                match event? {
                    Event::FocusGained => { terminal.focused = true; terminal.unseen = None; }
                    Event::FocusLost => terminal.focused = false,
                    Event::Paste(text) => ui.input.paste(&text),
                    Event::Key(event) => {
                        let quit = key(&mut ui, &mut session, event, Instant::now())?;
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

    fn shell_request(command: &str) -> RequestPermissionRequest {
        RequestPermissionRequest::new(
            "session",
            ToolCallUpdate::new(
                "shell-1",
                ToolCallUpdateFields::new()
                    .name("shell".to_owned())
                    .status(ToolCallStatus::Pending)
                    .raw_input(serde_json::json!({"command": command})),
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
        let request = shell_request("rg -n \\\n--glob '*.rs' \\\n'TODO|FIXME' src");
        let screen = Screen {
            approval: Some(Approval {
                request: &request,
                selected: 0,
            }),
            settings: "ask • deepseek/deepseek-v4-flash • high",
            usage: "5% • $0.01",
            ..screen(&view, &input, now)
        };
        let (rows, cursor, layout) = render(&screen, 72, 24);
        let rule = "─".repeat(72);
        assert_eq!(
            rows,
            [
                "Thought for 12s",
                "",
                "• Read Makefile",
                "• Find files matching *.rs",
                "",
                "$ ls -la",
                "",
                "Two tallies were counted in the workspace.",
                &rule,
                "",
                "Would you like to run the following command?",
                "",
                "$ rg -n \\",
                "  --glob '*.rs' \\",
                "  'TODO|FIXME' src",
                "",
                "› 1. Yes",
                "  2. No",
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
        assert_eq!(cursor, (19, 21));
        assert_eq!((layout.height, layout.lines), (8, 10));
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

    /// Collects the `tools` script's two permission requests.
    async fn requests(session: &mut Session, events: &mut UnboundedReceiver<AcpEvent>) {
        while session.pending.len() < 2 {
            if let AcpEvent::Permission(request, responder) = events.recv().await.unwrap() {
                session.permission(request, responder).unwrap();
            }
        }
    }

    #[tokio::test]
    async fn approval_keys_move_the_selection_and_enter_answers_only_with_an_empty_input() {
        with_session(async |mut session, mut events| {
            let now = Instant::now();
            let mut ui = Ui::default();
            let press = |ui: &mut Ui, session: &mut Session, code| {
                key(ui, session, KeyEvent::new(code, KeyModifiers::NONE), now)
            };
            session.prompt("tools".into())?;
            requests(&mut session, &mut events).await;
            press(&mut ui, &mut session, KeyCode::Down)?;
            press(&mut ui, &mut session, KeyCode::Down)?;
            assert_eq!(ui.selected, 1);
            press(&mut ui, &mut session, KeyCode::Esc)?;
            assert_eq!((session.pending.len(), ui.selected), (1, 0));
            press(&mut ui, &mut session, KeyCode::Up)?;
            press(&mut ui, &mut session, KeyCode::Down)?;
            press(&mut ui, &mut session, KeyCode::Enter)?;
            assert!(session.pending.is_empty());
            let (text, ok) = turn(&mut session, &mut events, &[]).await;
            assert!(ok);
            assert_eq!(text, "tally-1: stop, tally-2: stop");
            session.prompt("tools".into())?;
            requests(&mut session, &mut events).await;
            press(&mut ui, &mut session, KeyCode::Char('h'))?;
            press(&mut ui, &mut session, KeyCode::Char('i'))?;
            press(&mut ui, &mut session, KeyCode::Enter)?;
            assert!(session.pending.is_empty());
            assert_eq!(session.queued.as_deref(), Some("hi"));
            assert!(ui.input.is_empty());
            let queued = loop {
                match events.recv().await.unwrap() {
                    AcpEvent::Permission(request, responder) => {
                        session.permission(request, responder)?;
                    }
                    AcpEvent::Finished(_) => break session.finished()?,
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
