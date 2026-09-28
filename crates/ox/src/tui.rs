mod input;
mod theme;
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
    layout::{Margin, Rect},
    style::Style,
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

/// The rows the last draw gave the transcript view or the picker, and the
/// transcript view's line count, which paging uses.
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
    picker: Option<Picker>,
    resume_after_turn: bool,
}

struct Picker {
    rows: PickerRows,
    query: String,
    /// The indexes of the rows that match the query, in order.
    matches: Vec<usize>,
    /// An index into `matches`.
    selected: usize,
    first: usize,
    error: Option<String>,
}

/// The heading, the error line, the search input, and a blank line come
/// before a picker's rows.
const PICKER_TOP: usize = 4;

enum PickerRows {
    Sessions(Vec<SessionInfo>),
    Models(Vec<ModelChoice>),
}

struct ModelChoice {
    value: SessionConfigValueId,
    name: String,
    /// USD per million input tokens.
    input_price: Option<f64>,
    /// USD per million output tokens.
    output_price: Option<f64>,
    context_limit: Option<u64>,
}

impl ModelChoice {
    /// Reads the prices and context limit Ox ACP sends in the choice's `_meta`.
    fn new(choice: &SessionConfigSelectOption) -> Self {
        let meta = |key: &str| choice.meta.as_ref().and_then(|meta| meta.get(key));
        Self {
            value: choice.value.clone(),
            name: escape(&choice.name),
            input_price: meta("inputPrice").and_then(serde_json::Value::as_f64),
            output_price: meta("outputPrice").and_then(serde_json::Value::as_f64),
            context_limit: meta("contextLimit").and_then(serde_json::Value::as_u64),
        }
    }
}

impl Picker {
    fn new(rows: PickerRows) -> Self {
        let mut picker = Self {
            rows,
            query: String::new(),
            matches: Vec::new(),
            selected: 0,
            first: 0,
            error: None,
        };
        picker.filter();
        picker
    }

    /// Keeps the rows whose name contains every word of the query, ignoring
    /// case, and selects the first.
    fn filter(&mut self) {
        let words: Vec<_> = self
            .query
            .split_whitespace()
            .map(str::to_lowercase)
            .collect();
        let names: Vec<_> = match &self.rows {
            PickerRows::Sessions(sessions) => sessions.iter().map(session_title).collect(),
            PickerRows::Models(models) => models.iter().map(|model| model.name.clone()).collect(),
        };
        self.matches = names
            .iter()
            .enumerate()
            .filter(|(_, name)| {
                let name = name.to_lowercase();
                words.iter().all(|word| name.contains(word))
            })
            .map(|(index, _)| index)
            .collect();
        self.selected = 0;
        self.first = 0;
    }

    /// Selects a match and scrolls so it is among the `rows` shown.
    fn move_to(&mut self, selected: usize, rows: usize) {
        self.selected = selected.min(self.matches.len().saturating_sub(1));
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

fn session_title(session: &SessionInfo) -> String {
    escape(session.title.as_deref().unwrap_or("Untitled session"))
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
    picker: Option<&'a Picker>,
    pub settings: &'a str,
    pub usage: &'a str,
    pub show_thinking: bool,
    pub now: Instant,
}

/// Every region pads its content by one row above and below and two columns
/// on each side.
const MARGIN: Margin = Margin::new(2, 1);

fn put(buf: &mut Buffer, area: Rect, y: usize, line: &Line) {
    if let Ok(y) = u16::try_from(y)
        && y < area.height
    {
        buf.set_line(area.x, area.y + y, line, area.width);
    }
}

/// Places the cursor at column `x` of row `y` in the area, kept inside it.
fn cursor(frame: &mut Frame, area: Rect, x: usize, y: usize) {
    let x = u16::try_from(x)
        .unwrap_or(u16::MAX)
        .min(area.width.saturating_sub(1));
    let y = u16::try_from(y)
        .unwrap_or(u16::MAX)
        .min(area.height.saturating_sub(1));
    frame.set_cursor_position((area.x + x, area.y + y));
}

/// `left`, at least two spaces, and `right` ending at `width`, with `left`
/// clipped to fit.
fn justified(left: &str, right: &str, width: usize) -> String {
    let left = transcript::clip(left, width.saturating_sub(right.width() + 2));
    let padding = " ".repeat(width.saturating_sub(left.width() + right.width()));
    format!("{left}{padding}{right}")
}

pub fn draw(frame: &mut Frame, screen: &Screen) -> Layout {
    let area = frame.area();
    let buf = frame.buffer_mut();
    // Unstyled cells would show the terminal's own colors.
    buf.set_style(area, Style::new().fg(theme::TEXT).bg(theme::BACKGROUND));
    let height = usize::from(area.height);
    let width = usize::from(area.inner(MARGIN).width);
    // A picker fills the screen, hiding the approval dialog and the composer,
    // and the cursor sits in its search input.
    if let Some(picker) = screen.picker {
        let rows = height.saturating_sub(PICKER_TOP);
        let lines = picker_lines(picker, usize::from(area.width), rows);
        for (y, line) in lines.iter().enumerate() {
            put(buf, area, y, line);
        }
        cursor(frame, area, 2 + picker.query.width(), 2);
        return Layout {
            height: rows,
            lines: 0,
        };
    }
    let input = screen.input.rows(width);
    let approval = screen
        .approval
        .as_ref()
        .map(|approval| approval_lines(screen.view, approval, width));
    let dialog_height = approval.as_ref().map_or(0, |lines| lines.len() + 2);
    // The composer holds the input, a blank row, and the status line.
    let composer_height = input.lines.len() + 4;
    let space = height.saturating_sub(dialog_height + composer_height);
    // `rows` rows from row `y`, clipped to the screen.
    let region = |y: usize, rows: usize| {
        let top = y.min(height);
        Rect {
            y: area.y + top as u16,
            height: rows.min(height - top) as u16,
            ..area
        }
    };
    let view = region(0, space);
    let dialog = region(space, dialog_height);
    let composer = region(space + dialog_height, composer_height);
    buf.set_style(dialog, Style::new().bg(theme::APPROVAL));
    buf.set_style(composer, Style::new().bg(theme::COMPOSER));
    let view = view.inner(MARGIN);
    let view_height = usize::from(view.height);
    let lines = screen.view.lines(width, screen.show_thinking, screen.now);
    let first = screen.view.first_row(view_height, lines.len());
    for (y, line) in lines.iter().skip(first).take(view_height).enumerate() {
        put(buf, view, y, line);
    }
    if screen.view.new_activity && view_height > 0 {
        let notice = format!("{:^width$}", "new activity");
        let notice = Line::styled(notice, Style::new().fg(theme::LIGHT_YELLOW));
        put(buf, view, view_height - 1, &notice);
    }
    let dialog = dialog.inner(MARGIN);
    for (y, line) in approval.iter().flatten().enumerate() {
        put(buf, dialog, y, line);
    }
    let composer = composer.inner(MARGIN);
    for (y, line) in input.lines.iter().enumerate() {
        put(buf, composer, y, &Line::raw(line.as_str()));
    }
    let status = justified(screen.settings, screen.usage, width);
    put(buf, composer, input.lines.len() + 1, &Line::raw(status));
    let (row, column) = input.cursor;
    cursor(frame, composer, column, row);
    Layout {
        height: view_height,
        lines: lines.len(),
    }
}

fn picker_lines(picker: &Picker, width: usize, rows: usize) -> Vec<Line<'static>> {
    let heading = match picker.rows {
        PickerRows::Sessions(_) => "Resume a session",
        PickerRows::Models(_) => "Choose a model",
    };
    let error = picker.error.as_ref().map_or_else(Line::default, |error| {
        Line::styled(
            transcript::clip(error, width.saturating_sub(2)),
            Style::new().fg(theme::RED),
        )
    });
    let search = if picker.query.is_empty() {
        Line::styled("Search", Style::new().fg(theme::DIM))
    } else {
        Line::raw(picker.query.clone())
    };
    let mut lines = vec![Line::raw(heading), error, search, Line::default()];
    // Everything above the rows lines up with each row's content, which
    // follows its marker and a space.
    for line in &mut lines {
        line.spans.insert(0, Span::raw("  "));
    }
    // Each row follows its marker and a space and pads two columns on the
    // right.
    let names = match &picker.rows {
        PickerRows::Sessions(sessions) if sessions.is_empty() => {
            lines.push(Line::raw("  No saved sessions"));
            return lines;
        }
        PickerRows::Sessions(sessions) => session_rows(sessions, width.saturating_sub(4)),
        PickerRows::Models(models) => model_rows(models, width.saturating_sub(4)),
    };
    for (index, &row) in picker
        .matches
        .iter()
        .enumerate()
        .skip(picker.first)
        .take(rows)
    {
        let marker = if index == picker.selected { "›" } else { " " };
        let row = format!("{marker} {}", names[row]);
        let color = if index == picker.selected {
            theme::BRIGHT
        } else {
            theme::GRAY
        };
        lines.push(Line::styled(row, Style::new().fg(color)));
    }
    lines
}

fn session_rows(sessions: &[SessionInfo], width: usize) -> Vec<String> {
    sessions
        .iter()
        .map(|session| {
            let date = activity(session).map_or_else(
                || "Unknown date".to_owned(),
                |date| date.format("%Y-%m-%d").to_string(),
            );
            justified(&session_title(session), &date, width)
        })
        .collect()
}

/// Both price columns share the widest price's width plus two spaces.
fn model_rows(models: &[ModelChoice], width: usize) -> Vec<String> {
    let price = |price: Option<f64>| {
        price
            .map(|price| format!("${price:.2}"))
            .unwrap_or_default()
    };
    let limit = |model: &ModelChoice| model.context_limit.map(thousands).unwrap_or_default();
    let price_width = models
        .iter()
        .flat_map(|model| [price(model.input_price), price(model.output_price)])
        .map(|price| price.width())
        .max()
        .unwrap_or(0)
        + 2;
    let limit_width = models
        .iter()
        .map(|model| limit(model).width())
        .max()
        .unwrap_or(0);
    models
        .iter()
        .map(|model| {
            let prices = format!(
                "{:<price_width$}{:<price_width$}{:>limit_width$}",
                price(model.input_price),
                price(model.output_price),
                limit(model),
            );
            justified(&model.name, &prices, width)
        })
        .collect()
}

/// Formats a number with commas between thousands.
fn thousands(value: u64) -> String {
    let digits = value.to_string();
    let mut text = String::new();
    for (index, digit) in digits.chars().enumerate() {
        if index > 0 && (digits.len() - index).is_multiple_of(3) {
            text.push(',');
        }
        text.push(digit);
    }
    text
}

fn approval_lines(view: &TranscriptView, approval: &Approval, width: usize) -> Vec<Line<'static>> {
    let request = approval.request;
    let id = &request.tool_call.tool_call_id;
    let mut call = view
        .tool(id)
        .cloned()
        .unwrap_or_else(|| ToolCall::new(id.clone(), ""));
    call.update(request.tool_call.fields.clone());
    // Shell content names the subagent, if any, and everything being approved.
    let (heading, body) = match call.name.as_deref() {
        Some("shell") => (
            "Would you like to run the following command?",
            transcript::content_lines(&call, width, Style::new()),
        ),
        Some("shell_process") => (
            "Would you like to send the following input?",
            transcript::content_lines(&call, width, Style::new()),
        ),
        _ => (
            "Would you like to allow the following?",
            transcript::described_lines(&call, width, Style::new()),
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
    lines
}

/// The select option in the category, with its id.
fn select_option(
    options: &[SessionConfigOption],
    category: SessionConfigOptionCategory,
) -> Option<(&SessionConfigId, &SessionConfigSelect)> {
    let option = options
        .iter()
        .find(|option| option.category.as_ref() == Some(&category))?;
    match &option.kind {
        SessionConfigKind::Select(select) => Some((&option.id, select)),
        _ => None,
    }
}

/// The choices of a select option, with any groups flattened.
fn choices(select: &SessionConfigSelect) -> Vec<&SessionConfigSelectOption> {
    match &select.options {
        SessionConfigSelectOptions::Ungrouped(choices) => choices.iter().collect(),
        SessionConfigSelectOptions::Grouped(groups) => groups
            .iter()
            .flat_map(|group| group.options.iter())
            .collect(),
        _ => Vec::new(),
    }
}

/// The status line's left side: the mode, model, and effort in use.
pub fn settings(options: &[SessionConfigOption]) -> String {
    use SessionConfigOptionCategory::*;
    [Mode, Model, ThoughtLevel]
        .into_iter()
        .filter_map(|category| {
            select_option(options, category).map(|(_, select)| select.current_value.to_string())
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
    if let Some(picker) = &mut ui.picker {
        let rows = ui.layout.height;
        // The session picker stays open until a session is active.
        let closable = matches!(picker.rows, PickerRows::Models(_)) || session.active();
        match key.code {
            KeyCode::Up => picker.move_to(picker.selected.saturating_sub(1), rows),
            KeyCode::Down => picker.move_to(picker.selected.saturating_add(1), rows),
            KeyCode::PageUp => picker.move_to(picker.selected.saturating_sub(rows), rows),
            KeyCode::PageDown => picker.move_to(picker.selected.saturating_add(rows), rows),
            KeyCode::Home => picker.move_to(0, rows),
            KeyCode::End => picker.move_to(picker.matches.len().saturating_sub(1), rows),
            KeyCode::Esc if closable => ui.picker = None,
            KeyCode::Enter => match (&picker.rows, picker.matches.get(picker.selected)) {
                (_, None) => {}
                (PickerRows::Sessions(sessions), Some(&index)) => {
                    let id = sessions[index].session_id.clone();
                    if session.active()
                        && let Err(error) = session.close().await
                    {
                        picker.error = Some(format!("Close failed: {error}"));
                        return Ok(false);
                    }
                    ui.view = TranscriptView::default();
                    match session.load(id).await {
                        Ok(()) => {
                            ui.picker = None;
                            ui.input.clear();
                        }
                        Err(error) => picker.error = Some(format!("Load failed: {error}")),
                    }
                }
                (PickerRows::Models(models), Some(&index)) => {
                    let value = models[index].value.clone();
                    let Some((id, _)) =
                        select_option(&session.config_options, SessionConfigOptionCategory::Model)
                    else {
                        picker.error = Some("Model choice is unavailable".to_owned());
                        return Ok(false);
                    };
                    match session.set_config_option(id.clone(), value).await {
                        Ok(()) => ui.picker = None,
                        Err(error) => picker.error = Some(format!("Model change failed: {error}")),
                    }
                }
            },
            KeyCode::Char('c' | 'd') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                return Ok(true);
            }
            KeyCode::Char(c)
                if !key
                    .modifiers
                    .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT) =>
            {
                picker.query.push(c);
                picker.filter();
            }
            KeyCode::Backspace => {
                picker.query.pop();
                picker.filter();
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
                    .notice(format!("Mode change failed: {error}"), theme::RED, now);
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
                            .notice("Session resume is unavailable".into(), theme::RED, now);
                    } else if session.busy {
                        session.queued = None;
                        ui.resume_after_turn = true;
                        session.cancel()?;
                    } else {
                        open_session_picker(ui, session, now).await;
                    }
                } else if ui.input.text() == "/model" {
                    ui.input.clear();
                    open_model_picker(ui, session, now);
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

async fn open_session_picker(ui: &mut Ui, session: &mut Session, now: Instant) {
    match session.list().await {
        Ok(sessions) => {
            ui.picker = Some(Picker::new(PickerRows::Sessions(sorted_sessions(sessions))))
        }
        Err(error) => ui
            .view
            .notice(format!("Session list failed: {error}"), theme::RED, now),
    }
}

/// Opens the model picker with the current model selected.
fn open_model_picker(ui: &mut Ui, session: &Session, now: Instant) {
    let Some((_, select)) =
        select_option(&session.config_options, SessionConfigOptionCategory::Model)
    else {
        ui.view
            .notice("Model choice is unavailable".into(), theme::RED, now);
        return;
    };
    let models: Vec<_> = choices(select).into_iter().map(ModelChoice::new).collect();
    let current = models
        .iter()
        .position(|model| model.value == select.current_value)
        .unwrap_or(0);
    let mut picker = Picker::new(PickerRows::Models(models));
    picker.move_to(current, ui.layout.height);
    ui.picker = Some(picker);
}

fn next_mode(
    options: &[SessionConfigOption],
    forward: bool,
) -> Option<(SessionConfigId, SessionConfigValueId)> {
    let (id, select) = select_option(options, SessionConfigOptionCategory::Mode)?;
    let choices = choices(select);
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
    Some((id.clone(), choices[next].value.clone()))
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
        acp::Event::Diagnostic(text) => ui.view.notice(text, theme::DIM, now),
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
                    .notice(format!("Turn error: {error}"), theme::RED, now);
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
        terminal.status(if let Some(picker) = &ui.picker {
            match picker.rows {
                PickerRows::Sessions(_) => "resume session",
                PickerRows::Models(_) => "choose model",
            }
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
            picker: ui.picker.as_ref(),
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
                    open_session_picker(&mut ui, &mut session, Instant::now()).await;
                }
            }
            _ = tick.tick() => {}
            event = keys.next() => {
                let Some(event) = event else { session.cancel()?; return Ok(()) };
                match event? {
                    Event::FocusGained => { terminal.focused = true; terminal.unseen = None; }
                    Event::FocusLost => terminal.focused = false,
                    Event::Paste(text) => match &mut ui.picker {
                        Some(picker) => {
                            picker.query.extend(text.chars().filter(|c| !c.is_control()));
                            picker.filter();
                        }
                        None => ui.input.paste(&text),
                    },
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

    /// Draws the screen and returns its rows, the cursor, the layout, and
    /// the buffer.
    fn render(
        screen: &Screen,
        width: u16,
        height: u16,
    ) -> (Vec<String>, (u16, u16), Layout, Buffer) {
        let mut terminal = ratatui::Terminal::new(TestBackend::new(width, height)).unwrap();
        let mut layout = Layout::default();
        terminal.draw(|frame| layout = draw(frame, screen)).unwrap();
        let cursor = terminal.get_cursor_position().unwrap();
        let buffer = terminal.backend().buffer().clone();
        let rows = (0..height)
            .map(|y| {
                (0..width)
                    .map(|x| buffer.cell((x, y)).unwrap().symbol())
                    .collect::<String>()
                    .trim_end()
                    .to_owned()
            })
            .collect();
        (rows, (cursor.x, cursor.y), layout, buffer)
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
            picker: None,
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
        let (rows, cursor, layout, buffer) = render(&screen, 72, 27);
        assert_eq!(
            rows,
            [
                "",
                "  ● Thought for 12s",
                "",
                "  ● Read Makefile",
                "  ● Find files matching *.rs",
                "  ● Shell ls -la",
                "",
                "  ● Two tallies were counted in the workspace.",
                "",
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
                "",
                "  ❯ Also check the docs",
                "    when you are done",
                "",
                &format!(
                    "  {:<58}5% • $0.01",
                    "ask • deepseek/deepseek-v4-flash • high"
                ),
                "",
            ]
        );
        assert_eq!(cursor, (21, 23));
        assert_eq!((layout.height, layout.lines), (7, 9));
        let colors = |x, y| {
            let cell = buffer.cell((x, y)).unwrap();
            (cell.fg, cell.bg)
        };
        assert_eq!(colors(0, 8), (theme::TEXT, theme::BACKGROUND));
        assert_eq!(colors(0, 9), (theme::TEXT, theme::APPROVAL));
        assert_eq!(colors(8, 16), (theme::TEXT, theme::APPROVAL));
        assert_eq!(colors(4, 22), (theme::TEXT, theme::COMPOSER));
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
            rows,
            [
                "Would you like to send the following input?",
                "",
                "  Shell process: p-1",
                "  ",
                "  Input:",
                "  ",
                "      y",
                "",
                "› 1. Yes",
                "  2. No",
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
        let (rows, _, layout, _) = render(&screen(&view, &input, now), 40, 13);
        assert_eq!((layout.height, layout.lines), (6, 39));
        assert_eq!(rows[6], "  ❯ message 19");
        view.page_up(layout.height, layout.lines);
        let (rows, _, _, _) = render(&screen(&view, &input, now), 40, 13);
        assert_eq!(rows[5..8], ["  ❯ message 16", "", ""]);
        view.user("message 20".to_owned(), now);
        let (rows, _, _, buffer) = render(&screen(&view, &input, now), 40, 13);
        assert_eq!(
            rows[..6],
            [
                "",
                "  ❯ message 14",
                "",
                "  ❯ message 15",
                "",
                "  ❯ message 16"
            ]
        );
        assert_eq!(rows[6], "              new activity");
        assert_eq!(buffer.cell((14, 6)).unwrap().fg, theme::LIGHT_YELLOW);
        view.end();
        let (rows, _, _, _) = render(&screen(&view, &input, now), 40, 13);
        assert_eq!(rows[6], "  ❯ message 20");
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
        let (rows, cursor, _, _) = render(&screen, 40, 7);
        assert_eq!(
            rows[5],
            format!("  {:<25}15% • $0.25", "ask • deepseek • high")
        );
        assert_eq!(rows[6], "");
        assert_eq!(rows[3], "  ❯");
        assert_eq!(cursor, (4, 3));
    }

    #[test]
    fn session_picker_sorts_dates_and_keeps_selection_visible() {
        let mut picker = Picker::new(PickerRows::Sessions(sorted_sessions(vec![
            SessionInfo::new("a", "/tmp")
                .title("older")
                .updated_at("2026-09-28T00:30:00+05:00"),
            SessionInfo::new("b", "/tmp")
                .title("newer")
                .updated_at("2026-09-27T23:00:00Z"),
            SessionInfo::new("c", "/tmp").title("undated"),
        ])));
        let PickerRows::Sessions(sessions) = &picker.rows else {
            unreachable!()
        };
        assert_eq!(
            sessions
                .iter()
                .map(|item| item.session_id.to_string())
                .collect::<Vec<_>>(),
            ["b", "a", "c"]
        );
        let view = TranscriptView::default();
        let input = Input::default();
        let layout = {
            let mut display = screen(&view, &input, Instant::now());
            display.picker = Some(&picker);
            let (rows, _, layout, _) = render(&display, 40, 5);
            assert_eq!(rows[4], format!("› {:<26}2026-09-27", "newer"));
            layout
        };
        picker.move_to(2, layout.height);
        let mut screen = screen(&view, &input, Instant::now());
        screen.picker = Some(&picker);
        let (rows, _, _, _) = render(&screen, 40, 5);
        assert!(
            rows.iter()
                .any(|row| row.contains("› undated") && row.contains("Unknown date"))
        );
    }

    #[test]
    fn model_picker_rows_share_price_columns_and_leave_missing_values_blank() {
        let choice = |value: &str, name: &str, meta: serde_json::Value| {
            let serde_json::Value::Object(meta) = meta else {
                unreachable!()
            };
            ModelChoice::new(&SessionConfigSelectOption::new(value.to_owned(), name).meta(meta))
        };
        let mut picker = Picker::new(PickerRows::Models(vec![
            choice(
                "flash",
                "DeepSeek: DeepSeek V4.1 Flash",
                serde_json::json!({"inputPrice": 0.03, "outputPrice": 0.6, "contextLimit": 1048576}),
            ),
            choice(
                "opus",
                "Anthropic: Claude Opus 5.5 with a much longer name",
                serde_json::json!({"inputPrice": 4, "outputPrice": 20, "contextLimit": 200000}),
            ),
            choice(
                "other",
                "Other server model",
                serde_json::json!({"inputPrice": "free", "contextLimit": 1.5}),
            ),
        ]));
        picker.move_to(1, 8);
        let view = TranscriptView::default();
        let input = Input::default();
        let screen = Screen {
            picker: Some(&picker),
            ..screen(&view, &input, Instant::now())
        };
        let (rows, cursor, _, _) = render(&screen, 60, 8);
        assert_eq!(
            rows,
            [
                "  Choose a model",
                "",
                "  Search",
                "",
                "  DeepSeek: DeepSeek V4.1 Flash  $0.03   $0.60   1,048,576",
                "› Anthropic: Claude Opus 5.5 w…  $4.00   $20.00    200,000",
                "  Other server model",
                "",
            ],
            "the picker hides the composer"
        );
        assert_eq!(
            cursor,
            (2, 2),
            "the cursor starts on the search placeholder"
        );
    }

    #[test]
    fn picker_draws_the_selected_row_in_white_and_the_others_in_gray() {
        let mut picker = Picker::new(PickerRows::Sessions(vec![
            SessionInfo::new("a", "/tmp").title("older"),
            SessionInfo::new("b", "/tmp").title("newer"),
        ]));
        picker.move_to(1, 6);
        let view = TranscriptView::default();
        let input = Input::default();
        let mut display = screen(&view, &input, Instant::now());
        display.picker = Some(&picker);
        let (_, _, _, buffer) = render(&display, 40, 6);
        assert_eq!(buffer.cell((0, 5)).unwrap().fg, theme::BRIGHT);
        assert_eq!(buffer.cell((0, 4)).unwrap().fg, theme::GRAY);
    }

    #[tokio::test]
    async fn picker_search_keeps_rows_containing_every_word_and_enter_chooses_a_match() {
        with_session(async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            let matches = |ui: &Ui| ui.picker.as_ref().unwrap().matches.clone();
            ui.input.paste("/model");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            for (typed, expected) in [("", vec![0, 1]), ("VISION goo", vec![1]), ("x", vec![])] {
                ui.picker.as_mut().unwrap().query.clear();
                for c in typed.chars() {
                    press(&mut ui, &mut session, KeyCode::Char(c), now).await?;
                }
                assert_eq!(matches(&ui), expected, "query {typed:?}");
            }
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.picker.is_some(), "Enter without a match does nothing");
            press(&mut ui, &mut session, KeyCode::Backspace, now).await?;
            for c in "gemma".chars() {
                press(&mut ui, &mut session, KeyCode::Char(c), now).await?;
            }
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.picker.is_none());
            assert_eq!(settings(&session.config_options), "ask • gemma");
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn model_keys_choose_a_model_or_cancel() {
        with_session(async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            let selected = |ui: &Ui| ui.picker.as_ref().map(|picker| picker.selected);
            ui.input.paste("/model");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert_eq!(selected(&ui), Some(0));
            press(&mut ui, &mut session, KeyCode::Down, now).await?;
            press(&mut ui, &mut session, KeyCode::Esc, now).await?;
            assert!(ui.picker.is_none());
            assert_eq!(settings(&session.config_options), "ask • deepseek");
            ui.input.paste("/model");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            press(&mut ui, &mut session, KeyCode::Down, now).await?;
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.picker.is_none());
            assert_eq!(settings(&session.config_options), "ask • gemma");
            ui.input.paste("/model");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert_eq!(selected(&ui), Some(1), "the current model is selected");
            assert!(ui.input.is_empty());
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn resume_keys_cancel_or_reload_the_current_session() {
        with_session(async |mut session, _events| {
            let old = session.id().clone();
            let mut ui = Ui::default();
            let now = Instant::now();
            ui.input.paste("/resume");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.picker.is_some());
            press(&mut ui, &mut session, KeyCode::Esc, now).await?;
            assert!(ui.picker.is_none());
            assert_eq!(session.id(), &old);
            ui.input.paste("/resume");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.picker.is_none());
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
            assert_eq!(settings(&session.config_options), "ask • deepseek");
            for (code, expected) in [
                (KeyCode::Tab, "auto • deepseek"),
                (KeyCode::Tab, "ask • deepseek"),
                (KeyCode::BackTab, "auto • deepseek"),
                (KeyCode::BackTab, "ask • deepseek"),
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
