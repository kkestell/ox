mod input;
mod theme;
mod transcript;

use std::io::{Stdout, Write, stdout};
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use agent_client_protocol::schema::v1::*;
use chrono::DateTime;
use crossterm::{
    cursor::Show,
    event::{
        DisableBracketedPaste, DisableFocusChange, DisableMouseCapture, EnableBracketedPaste,
        EnableFocusChange, EnableMouseCapture, Event, EventStream, KeyCode, KeyEvent, KeyEventKind,
        KeyModifiers, KeyboardEnhancementFlags, MouseEvent, MouseEventKind,
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
    style::{Modifier, Style},
    text::{Line, Span},
};
use tokio::sync::mpsc::UnboundedReceiver;
use unicode_width::UnicodeWidthStr;

use crate::acp::{self, Session};
use crate::config;
use input::Input;
use transcript::{ToolOutput, TranscriptView};

/// The text with each control character except newline and tab written as an
/// escape sequence, so terminal output cannot move the cursor or change modes.
pub fn escape_control_characters(text: &str) -> String {
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
        DisableMouseCapture,
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
            EnableFocusChange,
            EnableMouseCapture
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

/// The rows and line counts the last draw gave each pageable region.
#[derive(Clone, Copy, Default)]
pub struct Layout {
    pub height: usize,
    pub lines: usize,
    approval_height: usize,
    approval_body_lines: usize,
}

#[derive(Default)]
struct Ui {
    view: TranscriptView,
    input: Input,
    approval_selected: usize,
    show_thinking: bool,
    tool_output: ToolOutput,
    layout: Layout,
    approval_scroll: usize,
    picker: Option<Picker>,
    resume_after_turn: bool,
    /// The favorite model IDs, in the order they were added.
    favorites: Vec<String>,
    /// The config file favorites are saved to.
    config_path: PathBuf,
}

struct Picker {
    rows: PickerRows,
    query: String,
    /// The indexes of the shown rows that match the query, in the shown
    /// order.
    matches: Vec<usize>,
    /// An index into `matches`.
    selected: usize,
    first: usize,
    error: Option<String>,
}

/// The search input and the error line, blank when there is no error, come
/// before a picker's rows.
const PICKER_TOP: usize = 2;

/// The blank columns beside a picker and the blank rows above and below it.
const PICKER_PADDING: Margin = Margin::new(4, 2);

/// The rows a picker shows on a screen `height` rows tall.
fn picker_rows(height: u16) -> usize {
    usize::from(height)
        .saturating_sub(usize::from(PICKER_PADDING.vertical) * 2)
        .saturating_sub(PICKER_TOP)
}

enum PickerRows {
    Sessions(Vec<SessionInfo>),
    Models(ModelRows),
}

/// The model picker's two lists. All is every choice the session offers, in
/// its order. Favorites is the favorite models the session offers, in the
/// order they were added.
struct ModelRows {
    all: Vec<ModelChoice>,
    /// Indexes into `all`.
    favorites: Vec<usize>,
    showing_favorites: bool,
}

impl ModelRows {
    fn new(all: Vec<ModelChoice>, favorites: &[String]) -> Self {
        let mut rows = Self {
            all,
            favorites: Vec::new(),
            showing_favorites: false,
        };
        rows.set_favorites(favorites);
        rows.showing_favorites = rows.has_favorites();
        rows
    }

    /// Replaces the favorites, showing All when none is offered.
    fn set_favorites(&mut self, favorites: &[String]) {
        self.favorites = favorites
            .iter()
            .filter_map(|id| self.all.iter().position(|model| &*model.value.0 == id))
            .collect();
        self.showing_favorites &= self.has_favorites();
    }

    /// Whether the picker offers the Favorites list.
    fn has_favorites(&self) -> bool {
        !self.favorites.is_empty()
    }

    /// The indexes into `all` of the shown list, in order.
    fn shown(&self) -> Vec<usize> {
        if self.showing_favorites {
            self.favorites.clone()
        } else {
            (0..self.all.len()).collect()
        }
    }
}

struct ModelChoice {
    value: SessionConfigValueId,
    name: String,
    provider: Option<String>,
    /// USD per million input tokens.
    input_price: Option<f64>,
    /// USD per million output tokens.
    output_price: Option<f64>,
    context_limit: Option<u64>,
}

impl ModelChoice {
    /// Reads the provider, prices, and context limit Ox ACP sends in the
    /// choice's `_meta`.
    fn new(choice: &SessionConfigSelectOption) -> Self {
        let meta = |key: &str| choice.meta.as_ref().and_then(|meta| meta.get(key));
        Self {
            value: choice.value.clone(),
            name: escape_control_characters(&choice.name),
            provider: meta("provider")
                .and_then(serde_json::Value::as_str)
                .map(escape_control_characters),
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
        // The name of every row, and the indexes of the shown rows.
        let (names, shown): (Vec<String>, Vec<usize>) = match &self.rows {
            PickerRows::Sessions(sessions) => (
                sessions.iter().map(session_title).collect(),
                (0..sessions.len()).collect(),
            ),
            PickerRows::Models(models) => (
                models.all.iter().map(|model| model.name.clone()).collect(),
                models.shown(),
            ),
        };
        self.matches = shown
            .into_iter()
            .filter(|&index| {
                let name = names[index].to_lowercase();
                words.iter().all(|word| name.contains(word))
            })
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
    escape_control_characters(session.title.as_deref().unwrap_or("Untitled session"))
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
    pub view: &'a mut TranscriptView,
    pub input: &'a Input,
    pub commands: &'a [String],
    pub approval: Option<Approval<'a>>,
    approval_scroll: usize,
    picker: Option<&'a Picker>,
    pub settings: &'a str,
    pub usage: &'a str,
    pub show_thinking: bool,
    pub tool_output: ToolOutput,
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

pub fn draw(frame: &mut Frame, screen: &mut Screen) -> Layout {
    let area = frame.area();
    let buf = frame.buffer_mut();
    // Unstyled cells would show the terminal's own colors.
    buf.set_style(area, Style::new().fg(theme::TEXT).bg(theme::BACKGROUND));
    let height = usize::from(area.height);
    let width = usize::from(area.inner(MARGIN).width);
    // A picker fills the screen, hiding the approval dialog and the composer,
    // and the cursor sits in its search input.
    if let Some(picker) = screen.picker {
        let padded = area.inner(PICKER_PADDING);
        let rows = picker_rows(area.height);
        let lines = picker_lines(picker, usize::from(padded.width), rows);
        for (y, line) in lines.iter().enumerate() {
            put(buf, padded, y, line);
        }
        cursor(frame, padded, picker.query.width(), 0);
        return Layout {
            height: rows,
            lines: 0,
            ..Layout::default()
        };
    }
    let input = screen.input.rows(width);
    let dialog_lines = screen
        .approval
        .as_ref()
        .map(|approval| approval_lines(screen.view, approval, width));
    // The composer holds the input, a blank row, and the status line.
    let composer_height = input.lines.len() + 4;
    let composer_top = height.saturating_sub(composer_height);
    let dialog_height = dialog_lines
        .as_ref()
        .map_or(0, |lines| (lines.len() + 2).min(composer_top));
    let space = composer_top - dialog_height;
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
    let composer = region(composer_top, composer_height);
    buf.set_style(dialog, Style::new().bg(theme::APPROVAL));
    buf.set_style(composer, Style::new().bg(theme::COMPOSER));
    let view = view.inner(MARGIN);
    let view_height = usize::from(view.height);
    let (total, lines) = screen.view.visible_rows(
        width,
        screen.show_thinking,
        screen.tool_output,
        screen.now,
        view_height,
    );
    for (y, line) in lines.iter().enumerate() {
        put(buf, view, y, line);
    }
    if screen.view.new_activity && view_height > 0 {
        let notice = format!("{:^width$}", "new activity");
        let notice = Line::styled(notice, Style::new().fg(theme::LIGHT_YELLOW));
        put(buf, view, view_height - 1, &notice);
    }
    let dialog = dialog.inner(MARGIN);
    let mut approval_height = 0;
    let mut approval_body_lines = 0;
    if let (Some(lines), Some(approval)) = (dialog_lines.as_ref(), screen.approval.as_ref()) {
        let option_count = approval.request.options.len();
        let body_end = lines.len() - option_count - 1;
        let body = &lines[2..body_end];
        approval_height = usize::from(dialog.height).saturating_sub(3 + option_count);
        approval_body_lines = body.len();
        let first = screen
            .approval_scroll
            .min(body.len().saturating_sub(approval_height));
        put(buf, dialog, 0, &lines[0]);
        for (y, line) in body.iter().skip(first).take(approval_height).enumerate() {
            put(buf, dialog, y + 2, line);
        }
        let footer = 2 + approval_height;
        if body.len() > approval_height {
            let hint = if first + approval_height < body.len() {
                "↓ Page Down for more"
            } else {
                "↑ Page Up for earlier"
            };
            put(
                buf,
                dialog,
                footer,
                &Line::styled(hint, Style::new().fg(theme::DIM)),
            );
        }
        for (y, line) in lines[body_end + 1..].iter().enumerate() {
            put(buf, dialog, footer + 1 + y, line);
        }
    }
    let composer = composer.inner(MARGIN);
    let ghost = screen.input.ghost_text(screen.commands).unwrap_or_default();
    for (y, line) in input.lines.iter().enumerate() {
        let mut spans = vec![Span::raw(line.as_str())];
        if y + 1 == input.lines.len() {
            spans.push(Span::styled(ghost, Style::new().fg(theme::DIM)));
        }
        put(buf, composer, y, &Line::from(spans));
    }
    let status = justified(screen.settings, screen.usage, width);
    let status = Line::styled(status, Style::new().fg(theme::GRAY));
    put(buf, composer, input.lines.len() + 1, &status);
    let (row, column) = input.cursor;
    cursor(frame, composer, column, row);
    Layout {
        height: view_height,
        lines: total,
        approval_height,
        approval_body_lines,
    }
}

fn picker_lines(picker: &Picker, width: usize, rows: usize) -> Vec<Line<'static>> {
    let error = picker.error.as_ref().map_or_else(Line::default, |error| {
        Line::styled(transcript::clip(error, width), Style::new().fg(theme::RED))
    });
    let mut search = if picker.query.is_empty() {
        vec![Span::styled("Search", Style::new().fg(theme::DIM))]
    } else {
        vec![Span::raw(picker.query.clone())]
    };
    // The model picker's list toggle ends at the search row's right edge.
    if let PickerRows::Models(models) = &picker.rows
        && models.has_favorites()
    {
        let (all, favorites) = if models.showing_favorites {
            (theme::DIM, theme::BRIGHT)
        } else {
            (theme::BRIGHT, theme::DIM)
        };
        let toggle_width = "Favorites / All".width();
        let padding = width.saturating_sub(search[0].width() + toggle_width);
        search.extend([
            Span::raw(" ".repeat(padding)),
            Span::styled("Favorites", Style::new().fg(favorites)),
            Span::styled(" / ", Style::new().fg(theme::DIM)),
            Span::styled("All", Style::new().fg(all)),
        ]);
    }
    let mut lines = vec![Line::from(search), error];
    if matches!(&picker.rows, PickerRows::Sessions(sessions) if sessions.is_empty()) {
        lines.push(Line::raw("No saved sessions"));
        return lines;
    }
    let names = match &picker.rows {
        PickerRows::Sessions(sessions) => session_rows(sessions, width),
        PickerRows::Models(models) => model_rows(&models.all, width),
    };
    for (index, &row) in picker
        .matches
        .iter()
        .enumerate()
        .skip(picker.first)
        .take(rows)
    {
        let color = if index == picker.selected {
            theme::BRIGHT
        } else {
            theme::GRAY
        };
        let mut style = Style::new().fg(color);
        // All shows favorites in bold.
        if let PickerRows::Models(models) = &picker.rows
            && !models.showing_favorites
            && models.favorites.contains(&row)
        {
            style = style.add_modifier(Modifier::BOLD);
        }
        lines.push(Line::styled(names[row].clone(), style));
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

/// The provider column reserves its widest value plus two spaces. Both price
/// columns share the widest price's width plus two spaces.
fn model_rows(models: &[ModelChoice], width: usize) -> Vec<String> {
    let price = |price: Option<f64>| {
        price
            .map(|price| format!("${price:.2}"))
            .unwrap_or_default()
    };
    let limit = |model: &ModelChoice| model.context_limit.map(thousands).unwrap_or_default();
    let provider_width = models
        .iter()
        .filter_map(|model| model.provider.as_deref())
        .map(UnicodeWidthStr::width)
        .max()
        .map_or(0, |width| width + 2);
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
            let provider = model.provider.as_deref().unwrap_or_default();
            let provider_padding = " ".repeat(provider_width.saturating_sub(provider.width()));
            let details = format!(
                "{provider}{provider_padding}{:<price_width$}{:<price_width$}{:>limit_width$}",
                price(model.input_price),
                price(model.output_price),
                limit(model),
            );
            justified(&model.name, &details, width)
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

/// The approval dialog's rows: the heading, a blank row, the body, a blank row,
/// and one row per option. `draw` slices the body and the options by position.
/// The request's tool call may carry only part of the call, so the transcript's
/// copy fills in the rest.
fn approval_lines(view: &TranscriptView, approval: &Approval, width: usize) -> Vec<Line<'static>> {
    let request = approval.request;
    let id = &request.tool_call.tool_call_id;
    let mut call = view
        .tool(id)
        .cloned()
        .unwrap_or_else(|| ToolCall::new(id.clone(), ""));
    call.update(request.tool_call.fields.clone());
    // Shell content names everything being approved.
    let (heading, body) = match call.name.as_deref() {
        Some("shell") => (
            "Would you like to run the following command?",
            transcript::content_lines(&call, width, "  ", "  ", Style::new()),
        ),
        Some("shell_process") => (
            "Would you like to send the following input?",
            transcript::content_lines(&call, width, "  ", "  ", Style::new()),
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

/// The select config option in the category, with its id.
fn select_config_option(
    config_options: &[SessionConfigOption],
    category: SessionConfigOptionCategory,
) -> Option<(&SessionConfigId, &SessionConfigSelect)> {
    let option = config_options
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

/// The status line's left side: the names of the mode, model, and effort in
/// use.
pub fn settings(config_options: &[SessionConfigOption]) -> String {
    use SessionConfigOptionCategory::*;
    [Mode, Model, ThoughtLevel]
        .into_iter()
        .filter_map(|category| {
            select_config_option(config_options, category).map(|(_, select)| {
                choices(select)
                    .into_iter()
                    .find(|choice| choice.value == select.current_value)
                    .map_or_else(
                        || select.current_value.to_string(),
                        |choice| choice.name.clone(),
                    )
            })
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

/// The slash command names the composer completes: the client's own and the
/// session's available commands, sorted.
fn commands(session: &Session) -> Vec<String> {
    let mut commands = session.commands.clone();
    commands.extend(["model", "new", "quit", "resume"].map(String::from));
    commands.sort();
    commands
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
            KeyCode::Left | KeyCode::Right => {
                if let PickerRows::Models(models) = &mut picker.rows
                    && models.has_favorites()
                {
                    models.showing_favorites = key.code == KeyCode::Left;
                    picker.filter();
                }
            }
            KeyCode::Char('f') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                if let PickerRows::Models(models) = &mut picker.rows
                    && let Some(&index) = picker.matches.get(picker.selected)
                {
                    let id = models.all[index].value.0.to_string();
                    let mut favorites = ui.favorites.clone();
                    match favorites.iter().position(|favorite| *favorite == id) {
                        Some(position) => {
                            favorites.remove(position);
                        }
                        None => favorites.push(id),
                    }
                    match config::save_favorites(&ui.config_path, &favorites) {
                        Ok(()) => {
                            models.set_favorites(&favorites);
                            ui.favorites = favorites;
                            let selected = picker.selected;
                            picker.filter();
                            picker.move_to(selected, rows);
                        }
                        Err(error) => picker.error = Some(format!("Favorite failed: {error:#}")),
                    }
                }
            }
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
                    let value = models.all[index].value.clone();
                    let Some((id, _)) = select_config_option(
                        &session.config_options,
                        SessionConfigOptionCategory::Model,
                    ) else {
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
    let approval_option_count = session
        .permission_request
        .as_ref()
        .map(|(request, _)| request.options.len());
    match key.code {
        KeyCode::Tab | KeyCode::BackTab
            if !key
                .modifiers
                .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT) =>
        {
            let forward = key.code == KeyCode::Tab && !key.modifiers.contains(KeyModifiers::SHIFT);
            let commands = commands(session);
            if forward && let Some(ghost) = ui.input.ghost_text(&commands) {
                ui.input.paste(ghost);
            } else if let Some((id, value)) = next_choice(
                &session.config_options,
                SessionConfigOptionCategory::Mode,
                forward,
            ) && let Err(error) = session.set_config_option(id, value).await
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
        KeyCode::Char('e' | 'E') if control => {
            if let Some((id, value)) = next_choice(
                &session.config_options,
                SessionConfigOptionCategory::ThoughtLevel,
                true,
            ) && let Err(error) = session.set_config_option(id, value).await
            {
                ui.view
                    .notice(format!("Effort change failed: {error}"), theme::RED, now);
            }
        }
        KeyCode::Char('o') if control => ui.tool_output = ui.tool_output.next(),
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
                } else if ui.input.text() == "/quit" {
                    session.cancel()?;
                    return Ok(true);
                } else if ui.input.text() == "/new" {
                    ui.input.clear();
                    if let Err(error) = new_session(ui, session).await {
                        ui.view
                            .notice(format!("New session failed: {error}"), theme::RED, now);
                    }
                } else if ui.input.text() == "/model" {
                    ui.input.clear();
                    open_model_picker(ui, session, now);
                } else if let Some(word) = ui.input.unknown_command(&commands(session)) {
                    ui.view
                        .notice(format!("Unknown command {word}"), theme::RED, now);
                } else {
                    send(ui, session, now)?;
                }
            } else if approval_option_count.is_some() {
                answer(ui, session, ui.approval_selected)?;
            }
        }
        KeyCode::Esc => {
            if let Some((request, _)) = session.permission_request.as_ref() {
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
        KeyCode::Up => match approval_option_count {
            Some(_) => ui.approval_selected = ui.approval_selected.saturating_sub(1),
            None => ui.input.up(),
        },
        KeyCode::Down => match approval_option_count {
            Some(count) => {
                ui.approval_selected = (ui.approval_selected + 1).min(count.saturating_sub(1))
            }
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
        KeyCode::PageUp if approval_option_count.is_some() => {
            let page = ui.layout.approval_height.saturating_sub(1).max(1);
            ui.approval_scroll = ui.approval_scroll.saturating_sub(page);
        }
        KeyCode::PageDown if approval_option_count.is_some() => {
            let page = ui.layout.approval_height.saturating_sub(1).max(1);
            let last = ui
                .layout
                .approval_body_lines
                .saturating_sub(ui.layout.approval_height);
            ui.approval_scroll = ui.approval_scroll.saturating_add(page).min(last);
        }
        KeyCode::PageUp => ui.view.page_up(ui.layout.height, ui.layout.lines),
        KeyCode::PageDown => ui.view.page_down(ui.layout.height, ui.layout.lines),
        _ => {}
    }
    Ok(false)
}

fn mouse(ui: &mut Ui, event: MouseEvent) {
    if ui.picker.is_some()
        || ui.layout.height == 0
        || usize::from(event.row) >= ui.layout.height + usize::from(MARGIN.vertical) * 2
    {
        return;
    }
    match event.kind {
        MouseEventKind::ScrollUp => ui.view.scroll_up(ui.layout.height, ui.layout.lines, 1),
        MouseEventKind::ScrollDown => ui.view.scroll_down(ui.layout.height, ui.layout.lines, 1),
        _ => {}
    }
}

async fn new_session(ui: &mut Ui, session: &mut Session) -> anyhow::Result<()> {
    if session.active() {
        session.close().await?;
    }
    ui.view = TranscriptView::default();
    session.create().await
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

/// Opens the model picker on Favorites when the session offers a favorite,
/// else on All, with the current model selected when it is shown.
fn open_model_picker(ui: &mut Ui, session: &Session, now: Instant) {
    let Some((_, select)) =
        select_config_option(&session.config_options, SessionConfigOptionCategory::Model)
    else {
        ui.view
            .notice("Model choice is unavailable".into(), theme::RED, now);
        return;
    };
    let models: Vec<_> = choices(select).into_iter().map(ModelChoice::new).collect();
    let current = models
        .iter()
        .position(|model| model.value == select.current_value);
    let mut picker = Picker::new(PickerRows::Models(ModelRows::new(models, &ui.favorites)));
    // The next draw scrolls it into view.
    picker.selected = picker
        .matches
        .iter()
        .position(|&index| Some(index) == current)
        .unwrap_or(0);
    ui.picker = Some(picker);
}

fn next_choice(
    config_options: &[SessionConfigOption],
    category: SessionConfigOptionCategory,
    forward: bool,
) -> Option<(SessionConfigId, SessionConfigValueId)> {
    let (id, select) = select_config_option(config_options, category)?;
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
    let busy = session.busy;
    if session.prompt(ui.input.text().to_owned())? {
        let text = ui.input.take();
        if !busy {
            ui.view.user(text, now);
        }
    }
    Ok(())
}

fn answer(ui: &mut Ui, session: &mut Session, index: usize) -> anyhow::Result<()> {
    session.answer(index + 1)?;
    ui.approval_selected = 0;
    ui.approval_scroll = 0;
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
            session.permission(request, responder)?;
            if session.permission_request.is_some() {
                ui.approval_selected = 0;
                ui.approval_scroll = 0;
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
    favorites: Vec<String>,
    config_path: PathBuf,
) -> anyhow::Result<()> {
    let previous = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        restore();
        previous(info);
    }));
    let mut terminal = Terminal::enter()?;
    let mut keys = EventStream::new();
    let mut ui = Ui {
        favorites,
        config_path,
        ..Ui::default()
    };
    let mut tick = tokio::time::interval(Duration::from_secs(1));
    loop {
        terminal.status(if let Some(picker) = &ui.picker {
            match picker.rows {
                PickerRows::Sessions(_) => "resume session",
                PickerRows::Models(_) => "choose model",
            }
        } else if session.permission_request.is_some() {
            "needs permission"
        } else if session.busy {
            "working"
        } else {
            terminal.unseen.unwrap_or("ready")
        })?;
        if let Some(picker) = &mut ui.picker {
            picker.move_to(picker.selected, picker_rows(terminal.inner.size()?.height));
        }
        let settings = settings(&session.config_options);
        let usage = usage(session.usage.as_ref());
        let commands = commands(&session);
        let mut screen = Screen {
            view: &mut ui.view,
            input: &ui.input,
            commands: &commands,
            approval: session
                .permission_request
                .as_ref()
                .map(|(request, _)| Approval {
                    request,
                    selected: ui.approval_selected,
                }),
            approval_scroll: ui.approval_scroll,
            picker: ui.picker.as_ref(),
            settings: &settings,
            usage: &usage,
            show_thinking: ui.show_thinking,
            tool_output: ui.tool_output,
            now: Instant::now(),
        };
        let mut layout = Layout::default();
        terminal
            .inner
            .draw(|frame| layout = draw(frame, &mut screen))?;
        ui.layout = layout;
        ui.approval_scroll = ui.approval_scroll.min(
            layout
                .approval_body_lines
                .saturating_sub(layout.approval_height),
        );
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
                    Event::Mouse(event) => mouse(&mut ui, event),
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
    use ox_server::fixture::{DEFAULT_MODEL, Reply, echo_reply, shell_reply};
    use ratatui::backend::TestBackend;
    use serde_json::json;

    #[test]
    fn server_control_characters_are_printed_as_text() {
        assert_eq!(
            escape_control_characters("\x1b[2J\r\x07\u{009b}31m\n\t"),
            "\\u{1b}[2J\\r\\u{7}\\u{9b}31m\n\t"
        );
    }

    #[test]
    fn mouse_wheel_moves_the_transcript_one_row_at_a_time() {
        let mut ui = Ui {
            layout: Layout {
                height: 10,
                lines: 35,
                ..Layout::default()
            },
            ..Ui::default()
        };
        let wheel = |kind, row| MouseEvent {
            kind,
            column: 4,
            row,
            modifiers: KeyModifiers::NONE,
        };
        mouse(&mut ui, wheel(MouseEventKind::ScrollUp, 3));
        assert_eq!(ui.view.first_row(10, 35), 24);
        mouse(&mut ui, wheel(MouseEventKind::ScrollUp, 3));
        assert_eq!(ui.view.first_row(10, 35), 23);
        ui.view.changed();
        assert!(ui.view.new_activity);
        mouse(&mut ui, wheel(MouseEventKind::ScrollDown, 3));
        assert_eq!(ui.view.first_row(10, 35), 24);
        mouse(&mut ui, wheel(MouseEventKind::ScrollDown, 3));
        assert_eq!(ui.view.first_row(10, 35), 25);
        assert!(!ui.view.new_activity);
        mouse(&mut ui, wheel(MouseEventKind::ScrollUp, 12));
        assert_eq!(ui.view.first_row(10, 35), 25);
        ui.picker = Some(Picker::new(PickerRows::Sessions(Vec::new())));
        mouse(&mut ui, wheel(MouseEventKind::ScrollUp, 3));
        assert_eq!(ui.view.first_row(10, 35), 25);
    }

    /// Draws the screen and returns its rows, the cursor, the layout, and
    /// the buffer.
    fn render(
        screen: &mut Screen,
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

    fn screen<'a>(view: &'a mut TranscriptView, input: &'a Input, now: Instant) -> Screen<'a> {
        Screen {
            view,
            input,
            commands: &[],
            approval: None,
            approval_scroll: 0,
            picker: None,
            settings: "",
            usage: "0% • $0.00",
            show_thinking: false,
            tool_output: ToolOutput::Summary,
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
        let mut screen = Screen {
            approval: Some(Approval {
                request: &request,
                selected: 0,
            }),
            settings: "ask • deepseek/deepseek-v4-flash • high",
            usage: "5% • $0.01",
            ..screen(&mut view, &input, now)
        };
        let (rows, cursor, layout, buffer) = render(&mut screen, 72, 27);
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

    #[tokio::test]
    async fn long_approval_keeps_options_and_composer_visible_and_pages_through_details() {
        with_session(
            vec![shell_reply(&[("true", 10)])],
            async |mut session, mut events| {
                session.prompt("run a command".into())?;
                collect_request(&mut session, &mut events).await;
                let details = (0..24)
                    .map(|line| format!("detail {line:02}"))
                    .collect::<Vec<_>>()
                    .join("\n");
                session.permission_request.as_mut().unwrap().0 = request("shell", &details);
                let mut ui = Ui::default();
                ui.input.paste("draft");
                let now = Instant::now();
                let show = |ui: &mut Ui, session: &Session| {
                    let request = &session.permission_request.as_ref().unwrap().0;
                    let mut display = Screen {
                        approval: Some(Approval {
                            request,
                            selected: ui.approval_selected,
                        }),
                        approval_scroll: ui.approval_scroll,
                        ..screen(&mut ui.view, &ui.input, now)
                    };
                    render(&mut display, 80, 24)
                };
                let (rows, cursor, layout, _) = show(&mut ui, &session);
                ui.layout = layout;
                assert!(rows.iter().any(|row| row.contains("detail 00")), "{rows:?}");
                assert!(rows.iter().any(|row| row.contains("Page Down for more")));
                assert!(rows.iter().any(|row| row.contains("› 1. Yes")), "{rows:?}");
                assert!(rows.iter().any(|row| row.contains("❯ draft")), "{rows:?}");
                assert!(cursor.1 < 24);
                press(&mut ui, &mut session, KeyCode::PageDown, now).await?;
                press(&mut ui, &mut session, KeyCode::PageDown, now).await?;
                let (rows, _, _, _) = show(&mut ui, &session);
                assert!(rows.iter().any(|row| row.contains("detail 23")), "{rows:?}");
                assert!(rows.iter().any(|row| row.contains("Page Up for earlier")));
                assert!(rows.iter().any(|row| row.contains("› 1. Yes")));
                press(&mut ui, &mut session, KeyCode::PageUp, now).await?;
                press(&mut ui, &mut session, KeyCode::PageUp, now).await?;
                let (rows, _, _, _) = show(&mut ui, &session);
                assert!(rows.iter().any(|row| row.contains("detail 00")));
                Ok(())
            },
        )
        .await;
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
                "",
                "  Input:",
                "",
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
        let (rows, _, layout, _) = render(&mut screen(&mut view, &input, now), 40, 13);
        assert_eq!((layout.height, layout.lines), (6, 39));
        assert_eq!(rows[6], "  ❯ message 19");
        view.page_up(layout.height, layout.lines);
        let (rows, _, _, _) = render(&mut screen(&mut view, &input, now), 40, 13);
        assert_eq!(rows[5..8], ["  ❯ message 16", "", ""]);
        view.user("message 20".to_owned(), now);
        let (rows, _, _, buffer) = render(&mut screen(&mut view, &input, now), 40, 13);
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
        let (rows, _, _, _) = render(&mut screen(&mut view, &input, now), 40, 13);
        assert_eq!(rows[6], "  ❯ message 20");
    }

    #[test]
    fn the_transcript_keeps_graphemes_at_row_boundaries() {
        let now = Instant::now();
        let input = Input::default();
        for (message, expected) in [("abcde👨‍👩‍👧‍👦", "👨‍👩‍👧‍👦"), ("abcde e\u{301}", "e\u{301}")]
        {
            let mut view = TranscriptView::default();
            view.update(
                SessionUpdate::AgentMessageChunk(ContentChunk::new(message.into())),
                now,
            );
            let mut display = screen(&mut view, &input, now);
            let (_, _, _, buffer) = render(&mut display, 12, 10);
            assert_eq!(buffer.cell((4, 2)).unwrap().symbol(), expected, "{message}");
        }
    }

    #[test]
    fn ghost_text_is_drawn_dim_after_the_cursor() {
        let mut view = TranscriptView::default();
        let mut input = Input::default();
        input.paste("/mo");
        let commands = ["model".to_owned()];
        let mut screen = Screen {
            commands: &commands,
            ..screen(&mut view, &input, Instant::now())
        };
        let (rows, cursor, _, buffer) = render(&mut screen, 20, 6);
        assert_eq!(rows[2], "  ❯ /model");
        assert_eq!(cursor, (7, 2));
        for x in 4..9 {
            let color = buffer.cell((x, 2)).unwrap().fg;
            let expected = if x < 7 { theme::TEXT } else { theme::DIM };
            assert_eq!(color, expected, "column {x}");
        }
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
        let config_options = vec![
            select("model", "deepseek", Some(Model)),
            select("effort", "high", Some(ThoughtLevel)),
            select("pace", "steady", None),
            select("approval", "ask", Some(Mode)),
        ];
        assert_eq!(settings(&config_options), "ask • deepseek • high");
        assert_eq!(settings(&config_options[..1]), "deepseek");
        assert_eq!(settings(&config_options[2..3]), "");
        assert_eq!(usage(None), "0% • $0.00");
        let update = UsageUpdate::new(1200, 8000).cost(Cost::new(0.25, "USD"));
        assert_eq!(usage(Some(&update)), "15% • $0.25");
        assert_eq!(usage(Some(&UsageUpdate::new(2, 3))), "67% • $0.00");
        let mut view = TranscriptView::default();
        let input = Input::default();
        let mut screen = Screen {
            settings: "ask • deepseek • high",
            usage: "15% • $0.25",
            ..screen(&mut view, &input, Instant::now())
        };
        let (rows, cursor, _, buffer) = render(&mut screen, 40, 7);
        assert_eq!(
            rows[5],
            format!("  {:<25}15% • $0.25", "ask • deepseek • high")
        );
        assert_eq!(buffer.cell((2, 5)).unwrap().fg, theme::GRAY);
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
        let mut view = TranscriptView::default();
        let input = Input::default();
        let layout = {
            let mut display = screen(&mut view, &input, Instant::now());
            display.picker = Some(&picker);
            let (rows, _, layout, _) = render(&mut display, 40, 7);
            assert_eq!(rows[4], format!("    {:<22}2026-09-27", "newer"));
            layout
        };
        picker.move_to(2, layout.height);
        let mut screen = screen(&mut view, &input, Instant::now());
        screen.picker = Some(&picker);
        let (rows, _, _, _) = render(&mut screen, 40, 7);
        assert!(
            rows.iter()
                .any(|row| row.contains("undated") && row.contains("Unknown date"))
        );
    }

    fn model_choice(value: &str, name: &str, meta: serde_json::Value) -> ModelChoice {
        let serde_json::Value::Object(meta) = meta else {
            unreachable!()
        };
        ModelChoice::new(&SessionConfigSelectOption::new(value.to_owned(), name).meta(meta))
    }

    #[test]
    fn model_picker_rows_align_provider_and_price_columns_and_leave_invalid_metadata_blank() {
        let models = vec![
            model_choice(
                "flash",
                "DeepSeek: DeepSeek V4.1 Flash",
                serde_json::json!({"provider": "OpenRouter", "inputPrice": 0.03, "outputPrice": 0.6, "contextLimit": 1048576}),
            ),
            model_choice(
                "opus",
                "Anthropic: Claude Opus 5.5 with a much longer name",
                serde_json::json!({"provider": "OpenAI", "inputPrice": 4, "outputPrice": 20, "contextLimit": 200000}),
            ),
            model_choice(
                "other",
                "Other server model",
                serde_json::json!({"provider": 7, "inputPrice": "free", "contextLimit": 1.5}),
            ),
            model_choice(
                "missing",
                "Missing provider",
                serde_json::json!({"inputPrice": 1.5, "contextLimit": 8001}),
            ),
        ];
        let mut picker = Picker::new(PickerRows::Models(ModelRows::new(models, &[])));
        picker.move_to(1, 4);
        let mut view = TranscriptView::default();
        let input = Input::default();
        let mut screen = Screen {
            picker: Some(&picker),
            ..screen(&mut view, &input, Instant::now())
        };
        let (rows, cursor, _, _) = render(&mut screen, 68, 13);
        assert_eq!(
            rows,
            [
                "",
                "",
                "    Search",
                "",
                "    DeepSeek: DeepSeek V…  OpenRouter  $0.03   $0.60   1,048,576",
                "    Anthropic: Claude Op…  OpenAI      $4.00   $20.00    200,000",
                "    Other server model",
                "    Missing provider                   $1.50               8,001",
                "",
                "",
                "",
                "",
                "",
            ],
            "the picker hides the composer"
        );
        assert_eq!(
            cursor,
            (4, 2),
            "the cursor starts on the search placeholder"
        );
    }

    #[test]
    fn model_picker_search_row_shows_the_active_list_in_white_and_all_shows_favorites_in_bold() {
        let models = vec![
            model_choice("flash", "Flash", serde_json::json!({})),
            model_choice("opus", "Opus", serde_json::json!({})),
        ];
        let favorites = ["opus".to_owned()];
        let mut picker = Picker::new(PickerRows::Models(ModelRows::new(models, &favorites)));
        let mut view = TranscriptView::default();
        let input = Input::default();
        for (showing_favorites, all, favorites, shown, bold) in [
            (
                false,
                theme::BRIGHT,
                theme::DIM,
                ["    Flash", "    Opus"],
                [false, true],
            ),
            (
                true,
                theme::DIM,
                theme::BRIGHT,
                ["    Opus", ""],
                [false, false],
            ),
        ] {
            let PickerRows::Models(models) = &mut picker.rows else {
                unreachable!()
            };
            models.showing_favorites = showing_favorites;
            picker.filter();
            let mut screen = Screen {
                picker: Some(&picker),
                ..screen(&mut view, &input, Instant::now())
            };
            let (rows, cursor, _, buffer) = render(&mut screen, 40, 8);
            assert_eq!(rows[2], format!("    {:<17}Favorites / All", "Search"));
            assert_eq!(cursor, (4, 2));
            let color = |x: u16| buffer.cell((x, 2)).unwrap().fg;
            assert_eq!(
                (color(21), color(31), color(33)),
                (favorites, theme::DIM, all),
                "showing_favorites {showing_favorites}"
            );
            assert_eq!(rows[4..6], shown);
            let is_bold = |y: u16| {
                buffer
                    .cell((4, y))
                    .unwrap()
                    .modifier
                    .contains(Modifier::BOLD)
            };
            assert_eq!(
                [is_bold(4), is_bold(5)],
                bold,
                "showing_favorites {showing_favorites}"
            );
        }
    }

    #[test]
    fn picker_scrolls_a_preselected_row_into_view() {
        let sessions = (0..10)
            .map(|index| SessionInfo::new(index.to_string(), "/tmp").title(format!("s{index}")))
            .collect();
        let mut picker = Picker::new(PickerRows::Sessions(sessions));
        picker.selected = 9;
        let mut view = TranscriptView::default();
        let input = Input::default();
        picker.move_to(picker.selected, picker_rows(12));
        let mut display = screen(&mut view, &input, Instant::now());
        display.picker = Some(&picker);
        let (rows, _, _, _) = render(&mut display, 40, 12);
        assert!(rows.iter().any(|row| row.contains("s9")), "{rows:?}");
    }

    #[test]
    fn picker_draws_the_selected_row_in_white_and_the_others_in_gray() {
        let mut picker = Picker::new(PickerRows::Sessions(vec![
            SessionInfo::new("a", "/tmp").title("older"),
            SessionInfo::new("b", "/tmp").title("newer"),
        ]));
        picker.move_to(1, 2);
        let mut view = TranscriptView::default();
        let input = Input::default();
        let mut display = screen(&mut view, &input, Instant::now());
        display.picker = Some(&picker);
        let (_, _, _, buffer) = render(&mut display, 40, 10);
        assert_eq!(buffer.cell((4, 5)).unwrap().fg, theme::BRIGHT);
        assert_eq!(buffer.cell((4, 4)).unwrap().fg, theme::GRAY);
        for (x, y) in [(0, 0), (39, 9), (20, 9)] {
            assert_eq!(buffer.cell((x, y)).unwrap().bg, theme::BACKGROUND);
        }
    }

    #[tokio::test]
    async fn picker_search_keeps_rows_containing_every_word_and_enter_chooses_a_match() {
        with_session(vec![], async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            let matches = |ui: &Ui| ui.picker.as_ref().unwrap().matches.clone();
            ui.input.paste("/model");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            for (typed, expected) in [
                ("", vec![0, 1, 2, 3, 4, 5]),
                ("FLASH glm", vec![1]),
                ("nonexistent", vec![]),
            ] {
                ui.picker.as_mut().unwrap().query.clear();
                for c in typed.chars() {
                    press(&mut ui, &mut session, KeyCode::Char(c), now).await?;
                }
                assert_eq!(matches(&ui), expected, "query {typed:?}");
            }
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.picker.is_some(), "Enter without a match does nothing");
            ui.picker.as_mut().unwrap().query.clear();
            for c in "glm".chars() {
                press(&mut ui, &mut session, KeyCode::Char(c), now).await?;
            }
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.picker.is_none());
            assert_eq!(
                settings(&session.config_options),
                "Ask • GLM 5.3 Flash • Default"
            );
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn model_keys_choose_a_model_or_cancel() {
        with_session(vec![], async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            let selected = |ui: &Ui| ui.picker.as_ref().map(|picker| picker.selected);
            ui.input.paste("/model");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert_eq!(selected(&ui), Some(0));
            press(&mut ui, &mut session, KeyCode::Down, now).await?;
            press(&mut ui, &mut session, KeyCode::Esc, now).await?;
            assert!(ui.picker.is_none());
            assert_eq!(
                settings(&session.config_options),
                "Ask • DeepSeek V4.1 Flash • Default"
            );
            ui.input.paste("/model");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            press(&mut ui, &mut session, KeyCode::Down, now).await?;
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.picker.is_none());
            assert_eq!(
                settings(&session.config_options),
                "Ask • GLM 5.3 Flash • Default"
            );
            ui.input.paste("/model");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert_eq!(selected(&ui), Some(1), "the current model is selected");
            assert!(ui.input.is_empty());
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn the_model_picker_opens_on_favorites_and_left_and_right_switch_lists() {
        with_session(vec![], async |mut session, _events| {
            let now = Instant::now();
            let mut ui = Ui {
                favorites: vec![
                    "openrouter:z-ai/glm-5.3-flash".to_owned(),
                    DEFAULT_MODEL.to_owned(),
                ],
                ..Ui::default()
            };
            let state = |ui: &Ui| {
                let picker = ui.picker.as_ref().unwrap();
                (picker.matches.clone(), picker.selected)
            };
            ui.input.paste("/model");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert_eq!(
                state(&ui),
                (vec![1, 0], 1),
                "opens on Favorites, in the order they were added, with the current model selected"
            );
            press(&mut ui, &mut session, KeyCode::Right, now).await?;
            assert_eq!(state(&ui), (vec![0, 1, 2, 3, 4, 5], 0), "Right shows All");
            for c in "deep".chars() {
                press(&mut ui, &mut session, KeyCode::Char(c), now).await?;
            }
            assert_eq!(state(&ui), (vec![0], 0));
            press(&mut ui, &mut session, KeyCode::Left, now).await?;
            assert_eq!(
                state(&ui),
                (vec![0], 0),
                "the query is kept across a switch"
            );
            for _ in 0..4 {
                press(&mut ui, &mut session, KeyCode::Backspace, now).await?;
            }
            assert_eq!(state(&ui), (vec![1, 0], 0));
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.picker.is_none());
            assert_eq!(
                settings(&session.config_options),
                "Ask • GLM 5.3 Flash • Default"
            );
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn the_model_picker_shows_only_all_without_an_offered_favorite() {
        with_session(vec![], async |mut session, _events| {
            let now = Instant::now();
            let mut view = TranscriptView::default();
            let input = Input::default();
            for favorites in [vec![], vec!["missing/model".to_owned()]] {
                let mut ui = Ui {
                    favorites: favorites.clone(),
                    ..Ui::default()
                };
                ui.input.paste("/model");
                press(&mut ui, &mut session, KeyCode::Enter, now).await?;
                press(&mut ui, &mut session, KeyCode::Left, now).await?;
                let picker = ui.picker.as_ref().unwrap();
                assert_eq!(picker.matches, [0, 1, 2, 3, 4, 5], "{favorites:?}");
                let mut screen = Screen {
                    picker: Some(picker),
                    ..screen(&mut view, &input, now)
                };
                let (rows, _, _, _) = render(&mut screen, 40, 8);
                assert_eq!(rows[2], "    Search", "{favorites:?}");
            }
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn control_f_favorites_and_unfavorites_the_selected_model_and_saves_the_favorites() {
        with_session(vec![], async |mut session, _events| {
            let now = Instant::now();
            let directory = tempfile::tempdir()?;
            let path = directory.path().join("ox/settings.json");
            let mut ui = Ui {
                favorites: vec!["missing/model".to_owned()],
                config_path: path.clone(),
                ..Ui::default()
            };
            let control_f = async |ui: &mut Ui, session: &mut Session| {
                let control_f = KeyEvent::new(KeyCode::Char('f'), KeyModifiers::CONTROL);
                key(ui, session, control_f, now).await
            };
            let state = |ui: &Ui| {
                let picker = ui.picker.as_ref().unwrap();
                let saved = config::Config::read(&path).unwrap().favorites;
                assert_eq!(saved, ui.favorites);
                (
                    ui.favorites.clone(),
                    picker.matches.clone(),
                    picker.selected,
                )
            };
            ui.input.paste("/model");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            press(&mut ui, &mut session, KeyCode::Down, now).await?;
            control_f(&mut ui, &mut session).await?;
            assert_eq!(
                state(&ui),
                (
                    vec![
                        "missing/model".into(),
                        "openrouter:z-ai/glm-5.3-flash".into(),
                    ],
                    vec![0, 1, 2, 3, 4, 5],
                    1
                ),
                "favoriting keeps the selection"
            );
            press(&mut ui, &mut session, KeyCode::Up, now).await?;
            control_f(&mut ui, &mut session).await?;
            press(&mut ui, &mut session, KeyCode::Left, now).await?;
            assert_eq!(
                state(&ui),
                (
                    vec![
                        "missing/model".into(),
                        "openrouter:z-ai/glm-5.3-flash".into(),
                        DEFAULT_MODEL.into()
                    ],
                    vec![1, 0],
                    0
                )
            );
            control_f(&mut ui, &mut session).await?;
            assert_eq!(
                state(&ui),
                (
                    vec!["missing/model".into(), DEFAULT_MODEL.into()],
                    vec![0],
                    0
                ),
                "unfavoriting removes the row from Favorites"
            );
            control_f(&mut ui, &mut session).await?;
            assert_eq!(
                state(&ui),
                (vec!["missing/model".into()], vec![0, 1, 2, 3, 4, 5], 0),
                "the last offered favorite's removal shows All"
            );
            assert!(ui.input.is_empty());
            ui.config_path = directory.path().into();
            control_f(&mut ui, &mut session).await?;
            let picker = ui.picker.as_ref().unwrap();
            assert!(
                picker
                    .error
                    .as_deref()
                    .is_some_and(|error| error.starts_with("Favorite failed: ")),
                "{:?}",
                picker.error
            );
            assert_eq!(ui.favorites, ["missing/model"]);
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn resume_keys_cancel_or_reload_the_current_session() {
        with_session(vec![], async |mut session, _events| {
            let old = session.id().clone();
            let mut ui = Ui::default();
            let now = Instant::now();
            ui.input.paste("/resume");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert!(ui.picker.is_some());
            press(&mut ui, &mut session, KeyCode::Right, now).await?;
            assert_eq!(ui.picker.as_ref().unwrap().matches, [0]);
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
    async fn new_command_replaces_the_session_and_quit_command_quits() {
        with_session(vec![], async |mut session, _events| {
            let old = session.id().clone();
            let mut ui = Ui::default();
            let now = Instant::now();
            ui.input.paste("/new");
            assert!(!press(&mut ui, &mut session, KeyCode::Enter, now).await?);
            assert!(session.active());
            assert_ne!(session.id(), &old);
            assert!(ui.input.is_empty());
            ui.input.paste("/quit");
            assert!(press(&mut ui, &mut session, KeyCode::Enter, now).await?);
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn enter_refuses_an_unknown_slash_command() {
        with_session(vec![], async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            ui.input.paste("/mo");
            press(&mut ui, &mut session, KeyCode::Enter, now).await?;
            assert_eq!(ui.input.text(), "/mo");
            assert!(!session.busy);
            let (rows, _, _, _) = render(&mut screen(&mut ui.view, &ui.input, now), 40, 10);
            assert!(
                rows.iter().any(|row| row.contains("Unknown command /mo")),
                "{rows:#?}"
            );
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn resume_wait_does_not_queue_another_prompt() {
        let hang = Reply::Hang(format!(
            "data: {}\n\n",
            ox_server::fixture::delta(json!({"role":"assistant", "content":"running"}), None)
        ));
        with_session(vec![hang], async |mut session, _events| {
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

    #[tokio::test]
    async fn second_submission_stays_in_the_composer_while_a_prompt_is_queued() {
        let hang = Reply::Hang(format!(
            "data: {}\n\n",
            ox_server::fixture::delta(json!({"role":"assistant", "content":"running"}), None)
        ));
        with_session(vec![hang, echo_reply()], async |mut session, mut events| {
            session.prompt("running".into())?;
            while !matches!(
                events.recv().await.unwrap(),
                AcpEvent::Update(_, SessionUpdate::AgentMessageChunk(_))
            ) {}
            let mut ui = Ui::default();
            let now = Instant::now();
            ui.input.paste("first");
            send(&mut ui, &mut session, now)?;
            assert!(ui.input.is_empty());
            ui.input.paste("second");
            send(&mut ui, &mut session, now)?;
            assert_eq!(ui.input.text(), "second");
            assert_eq!(session.queued.as_deref(), Some("first"));
            let queued = loop {
                if let AcpEvent::Finished(_, _) = events.recv().await.unwrap() {
                    break session.finished()?;
                }
            };
            assert_eq!(queued.as_deref(), Some("first"));
            assert_eq!(
                turn(&mut session, &mut events, &[]).await.0,
                "you said: first"
            );
            assert_eq!(ui.input.text(), "second");
            Ok(())
        })
        .await;
    }

    /// Collects the next permission request.
    async fn collect_request(session: &mut Session, events: &mut UnboundedReceiver<AcpEvent>) {
        while session.permission_request.is_none() {
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
    async fn control_t_toggles_thinking_and_control_o_cycles_tool_output() {
        with_session(vec![], async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            for (c, expected) in [
                ('o', (false, ToolOutput::Truncated)),
                ('t', (true, ToolOutput::Truncated)),
                ('o', (true, ToolOutput::Full)),
                ('o', (true, ToolOutput::Summary)),
            ] {
                key(
                    &mut ui,
                    &mut session,
                    KeyEvent::new(KeyCode::Char(c), KeyModifiers::CONTROL),
                    now,
                )
                .await?;
                assert_eq!((ui.show_thinking, ui.tool_output), expected, "Ctrl+{c}");
            }
            assert!(ui.input.is_empty());
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn tab_and_backtab_cycle_the_available_modes() {
        with_session(vec![], async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            assert_eq!(
                settings(&session.config_options),
                "Ask • DeepSeek V4.1 Flash • Default"
            );
            for (code, expected) in [
                (KeyCode::Tab, "Auto • DeepSeek V4.1 Flash • Default"),
                (KeyCode::Tab, "Ask • DeepSeek V4.1 Flash • Default"),
                (KeyCode::BackTab, "Auto • DeepSeek V4.1 Flash • Default"),
                (KeyCode::BackTab, "Ask • DeepSeek V4.1 Flash • Default"),
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
    async fn control_e_cycles_effort_with_or_without_shift() {
        with_session(vec![], async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            ui.input.paste("draft");
            for (code, modifiers, expected) in [
                ('e', KeyModifiers::CONTROL, "Low"),
                ('e', KeyModifiers::CONTROL | KeyModifiers::SHIFT, "Medium"),
                ('E', KeyModifiers::CONTROL | KeyModifiers::SHIFT, "High"),
            ] {
                key(
                    &mut ui,
                    &mut session,
                    KeyEvent::new(KeyCode::Char(code), modifiers),
                    now,
                )
                .await?;
                assert!(settings(&session.config_options).ends_with(expected));
                assert_eq!(ui.input.text(), "draft");
            }
            Ok(())
        })
        .await;
    }

    #[test]
    fn effort_cycle_ignores_missing_or_single_choices() {
        use SessionConfigOptionCategory::ThoughtLevel;
        assert!(next_choice(&[], ThoughtLevel, true).is_none());
        let only = SessionConfigOption::select(
            "effort",
            "Effort",
            "low",
            vec![SessionConfigSelectOption::new("low", "Low")],
        )
        .category(ThoughtLevel);
        assert!(next_choice(&[only], ThoughtLevel, true).is_none());
    }

    #[tokio::test]
    async fn tab_inserts_ghost_text_and_otherwise_cycles_the_mode() {
        with_session(vec![], async |mut session, _events| {
            let mut ui = Ui::default();
            let now = Instant::now();
            session.commands = vec!["tally".to_owned()];
            for c in "/ta".chars() {
                press(&mut ui, &mut session, KeyCode::Char(c), now).await?;
            }
            press(&mut ui, &mut session, KeyCode::BackTab, now).await?;
            assert!(settings(&session.config_options).starts_with("Auto"));
            assert_eq!(ui.input.text(), "/ta");
            press(&mut ui, &mut session, KeyCode::Tab, now).await?;
            assert!(settings(&session.config_options).starts_with("Auto"));
            assert_eq!(ui.input.text(), "/tally");
            press(&mut ui, &mut session, KeyCode::Tab, now).await?;
            assert!(settings(&session.config_options).starts_with("Ask"));
            assert_eq!(ui.input.text(), "/tally");
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn approval_keys_move_the_selection_and_enter_answers_only_with_an_empty_input() {
        with_session(
            vec![
                shell_reply(&[("true", 10)]),
                echo_reply(),
                shell_reply(&[("true", 10)]),
                echo_reply(),
                shell_reply(&[("true", 10)]),
                echo_reply(),
            ],
            async |mut session, mut events| {
                let now = Instant::now();
                let mut ui = Ui::default();
                session.prompt("run a command".into())?;
                collect_request(&mut session, &mut events).await;
                press(&mut ui, &mut session, KeyCode::Down, now).await?;
                press(&mut ui, &mut session, KeyCode::Down, now).await?;
                assert_eq!(ui.approval_selected, 1);
                press(&mut ui, &mut session, KeyCode::Esc, now).await?;
                assert_eq!(
                    (session.permission_request.is_none(), ui.approval_selected),
                    (true, 0)
                );
                assert!(turn(&mut session, &mut events, &[]).await.1);
                session.prompt("run another command".into())?;
                collect_request(&mut session, &mut events).await;
                press(&mut ui, &mut session, KeyCode::Up, now).await?;
                press(&mut ui, &mut session, KeyCode::Down, now).await?;
                press(&mut ui, &mut session, KeyCode::Enter, now).await?;
                assert!(session.permission_request.is_none());
                let (text, ok) = turn(&mut session, &mut events, &[]).await;
                assert!(ok);
                assert_eq!(text, "you said: run another command");
                session.prompt("run a third command".into())?;
                collect_request(&mut session, &mut events).await;
                press(&mut ui, &mut session, KeyCode::Char('h'), now).await?;
                press(&mut ui, &mut session, KeyCode::Char('i'), now).await?;
                press(&mut ui, &mut session, KeyCode::Enter, now).await?;
                assert!(session.permission_request.is_none());
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
            },
        )
        .await;
    }
}
