use unicode_segmentation::UnicodeSegmentation;
use unicode_width::UnicodeWidthStr;

const MAX_ROWS: usize = 8;

/// The composer's editable text with a byte cursor.
#[derive(Default)]
pub struct Input {
    text: String,
    cursor: usize,
}

/// The visible input rows, each with its prefix, and the cursor's row and
/// column among them.
pub struct Rows {
    pub lines: Vec<String>,
    pub cursor: (usize, usize),
}

impl Input {
    pub fn text(&self) -> &str {
        &self.text
    }

    pub fn is_empty(&self) -> bool {
        self.text.is_empty()
    }

    pub fn take(&mut self) -> String {
        self.cursor = 0;
        std::mem::take(&mut self.text)
    }

    pub fn clear(&mut self) {
        self.take();
    }

    pub fn insert(&mut self, c: char) {
        self.text.insert(self.cursor, c);
        self.cursor += c.len_utf8();
        self.next_boundary();
    }

    pub fn paste(&mut self, text: &str) {
        let text = text.replace("\r\n", "\n").replace('\r', "\n");
        self.text.insert_str(self.cursor, &text);
        self.cursor += text.len();
        self.next_boundary();
    }

    /// The rest of the first command name that the input, one word starting
    /// with `/` with the cursor at its end, is a strict prefix of.
    pub fn ghost_text<'a>(&self, commands: &'a [String]) -> Option<&'a str> {
        let word = self.text.strip_prefix('/')?;
        if word.is_empty() || self.cursor != self.text.len() || word.contains(char::is_whitespace) {
            return None;
        }
        let command = commands.iter().find(|command| command.starts_with(word))?;
        command.strip_prefix(word).filter(|rest| !rest.is_empty())
    }

    pub fn newline(&mut self) {
        self.insert('\n');
    }

    pub fn backspace(&mut self) {
        if let Some((start, _)) = self.text[..self.cursor].grapheme_indices(true).next_back() {
            self.text.drain(start..self.cursor);
            self.cursor = start;
        }
    }

    pub fn delete(&mut self) {
        if let Some(grapheme) = self.text[self.cursor..].graphemes(true).next() {
            self.text.drain(self.cursor..self.cursor + grapheme.len());
        }
    }

    pub fn left(&mut self) {
        if let Some((start, _)) = self.text[..self.cursor].grapheme_indices(true).next_back() {
            self.cursor = start;
        }
    }

    pub fn right(&mut self) {
        if let Some(grapheme) = self.text[self.cursor..].graphemes(true).next() {
            self.cursor += grapheme.len();
        }
    }

    /// Moves the cursor to the next grapheme boundary. Inserted text can join
    /// the following cluster, as when a base character lands before a
    /// combining mark, leaving the cursor inside it.
    fn next_boundary(&mut self) {
        self.cursor = self
            .text
            .grapheme_indices(true)
            .map(|(index, _)| index)
            .find(|&index| index >= self.cursor)
            .unwrap_or(self.text.len());
    }

    pub fn home(&mut self) {
        self.cursor = self.line_start();
    }

    pub fn end(&mut self) {
        self.cursor = self.line_end();
    }

    pub fn up(&mut self) {
        let column = self.column();
        let start = self.line_start();
        if start == 0 {
            return;
        }
        let previous = self.text[..start - 1].rfind('\n').map_or(0, |i| i + 1);
        self.cursor = self.seek(previous, column);
    }

    pub fn down(&mut self) {
        let column = self.column();
        let end = self.line_end();
        if end == self.text.len() {
            return;
        }
        self.cursor = self.seek(end + 1, column);
    }

    fn line_start(&self) -> usize {
        self.text[..self.cursor].rfind('\n').map_or(0, |i| i + 1)
    }

    fn line_end(&self) -> usize {
        self.text[self.cursor..]
            .find('\n')
            .map_or(self.text.len(), |i| self.cursor + i)
    }

    /// The cursor's display column within its logical line.
    fn column(&self) -> usize {
        self.text[self.line_start()..self.cursor]
            .graphemes(true)
            .map(|cluster| display_cluster(cluster).width())
            .sum()
    }

    /// The byte offset at the display column in the logical line starting at
    /// `start`, or the line's end.
    fn seek(&self, start: usize, column: usize) -> usize {
        let mut offset = start;
        let mut used = 0;
        for cluster in self.text[start..].graphemes(true) {
            let cluster_width = display_cluster(cluster).width();
            if cluster == "\n" || used + cluster_width > column {
                break;
            }
            used += cluster_width;
            offset += cluster.len();
        }
        offset
    }

    /// The rows to show at `width`, hard-wrapped by display width, at most
    /// eight rows, and scrolled to keep the cursor row visible.
    pub fn rows(&self, width: usize) -> Rows {
        let text_width = width.saturating_sub(2).max(1);
        let mut rows = Vec::new();
        let mut current = String::new();
        let mut column = 0;
        let mut cursor = (0, 0);
        let mut offset = 0;
        for (index, line) in self.text.split('\n').enumerate() {
            if index > 0 {
                rows.push(std::mem::take(&mut current));
                column = 0;
                offset += 1;
            }
            for cluster in line.graphemes(true) {
                let shown = display_cluster(cluster);
                let cluster_width = shown.width();
                if column + cluster_width > text_width && column > 0 {
                    rows.push(std::mem::take(&mut current));
                    column = 0;
                }
                if offset == self.cursor {
                    cursor = (rows.len(), column);
                }
                current.push_str(&shown);
                column += cluster_width;
                offset += cluster.len();
            }
            if offset == self.cursor {
                if column >= text_width {
                    rows.push(std::mem::take(&mut current));
                    column = 0;
                }
                cursor = (rows.len(), column);
            }
        }
        rows.push(current);
        let height = rows.len().min(MAX_ROWS);
        let scroll = cursor.0.saturating_sub(height - 1).min(rows.len() - height);
        let lines = rows
            .into_iter()
            .enumerate()
            .skip(scroll)
            .take(height)
            .map(|(index, row)| {
                let prefix = if index == 0 { "❯ " } else { "  " };
                format!("{prefix}{row}")
            })
            .collect();
        Rows {
            lines,
            cursor: (cursor.0 - scroll, cursor.1 + 2),
        }
    }
}

/// How one character is shown: tabs as an arrow and other control
/// characters as their escapes.
fn display(c: char) -> String {
    if c == '\t' {
        "→".to_owned()
    } else if c.is_control() {
        c.escape_default().to_string()
    } else {
        c.to_string()
    }
}

fn display_cluster(cluster: &str) -> String {
    cluster.chars().map(display).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn typed(text: &str) -> Input {
        let mut input = Input::default();
        for c in text.chars() {
            input.insert(c);
        }
        input
    }

    #[test]
    fn editing_keys_move_the_cursor_and_change_the_text() {
        let mut input = typed("ab");
        input.left();
        input.insert('x');
        assert_eq!(input.text(), "axb");
        input.home();
        input.delete();
        assert_eq!(input.text(), "xb");
        input.end();
        input.backspace();
        assert_eq!(input.text(), "x");
        input.right();
        input.newline();
        for c in "界y".chars() {
            input.insert(c);
        }
        assert_eq!(input.text(), "x\n界y");
        input.up();
        input.insert('!');
        assert_eq!(input.text(), "x!\n界y");
        input.down();
        input.insert('?');
        assert_eq!(input.text(), "x!\n界?y");
        input.end();
        input.up();
        input.insert('.');
        assert_eq!(input.text(), "x!.\n界?y");
        input.down();
        input.down();
        input.insert('$');
        assert_eq!(input.text(), "x!.\n界?$y");
        input.clear();
        assert!(input.is_empty());
        input.backspace();
        input.left();
        input.up();
        assert_eq!(input.take(), "");
    }

    #[test]
    fn paste_normalizes_line_endings_and_waits_for_enter() {
        let mut input = typed("ad");
        input.left();
        input.paste("b\r\nc\r");
        assert_eq!(input.text(), "ab\nc\nd");
        input.insert('!');
        assert_eq!(input.take(), "ab\nc\n!d");
        assert!(input.is_empty());
    }

    #[test]
    fn ghost_text_completes_a_lone_slash_word_at_the_end_of_the_input() {
        let commands = ["compact", "model", "resume"].map(String::from);
        let mut moved = typed("/mo");
        moved.left();
        for (input, expected) in [
            (typed(""), None),
            (typed("/"), None),
            (typed("/mo"), Some("del")),
            (typed("/model"), None),
            (typed("/model x"), None),
            (moved, None),
            (typed("\n/mo"), None),
            (typed("hi /mo"), None),
            (typed("/zzz"), None),
        ] {
            assert_eq!(input.ghost_text(&commands), expected, "{:?}", input.text());
        }
        let overlapping = ["model", "model-fast"].map(String::from);
        assert_eq!(typed("/mode").ghost_text(&overlapping), Some("l"));
        assert_eq!(typed("/model").ghost_text(&overlapping), None);
        let mut input = typed("/mo");
        input.paste(input.ghost_text(&commands).unwrap());
        assert_eq!(input.text(), "/model");
    }

    #[test]
    fn rows_wrap_at_the_width_and_keep_the_cursor_visible() {
        let rows = |input: &Input, width| {
            let rows = input.rows(width);
            (rows.lines, rows.cursor)
        };
        let mut input = typed("abcdefgh");
        assert_eq!(
            rows(&input, 6),
            (vec!["❯ abcd".into(), "  efgh".into(), "  ".into()], (2, 2))
        );
        input.home();
        assert_eq!(
            rows(&input, 6),
            (vec!["❯ abcd".into(), "  efgh".into()], (0, 2))
        );
        let tabbed = typed("a\tb");
        assert_eq!(rows(&tabbed, 20), (vec!["❯ a→b".into()], (0, 5)));
        let mut tall = typed("0\n1\n2\n3\n4\n5\n6\n7\n8\n9");
        let (lines, cursor) = rows(&tall, 20);
        assert_eq!(lines.len(), 8);
        assert_eq!(lines[0], "  2");
        assert_eq!(lines[7], "  9");
        assert_eq!(cursor, (7, 3));
        for _ in 0..9 {
            tall.up();
        }
        let (lines, cursor) = rows(&tall, 20);
        assert_eq!(lines[0], "❯ 0");
        assert_eq!(lines[7], "  7");
        assert_eq!(cursor, (0, 3));
        assert_eq!(rows(&Input::default(), 20), (vec!["❯ ".into()], (0, 2)));
    }

    #[test]
    fn joined_emoji_and_combining_marks_are_single_editing_units() {
        let mut input = Input::default();
        input.paste("a👨‍👩‍👧‍👦e\u{301}b");
        input.home();
        input.right();
        input.right();
        assert_eq!(input.rows(12).cursor, (0, 5));
        input.left();
        input.delete();
        assert_eq!(input.text(), "ae\u{301}b");
        input.right();
        input.backspace();
        assert_eq!(input.text(), "ab");

        let mut input = Input::default();
        input.paste("a👨‍👩‍👧‍👦b\nxy");
        input.up();
        input.insert('!');
        assert_eq!(input.text(), "a!👨‍👩‍👧‍👦b\nxy");
        input.down();
        input.insert('?');
        assert_eq!(input.text(), "a!👨‍👩‍👧‍👦b\nxy?");

        let mut input = Input::default();
        input.paste("e");
        input.paste("\u{301}");
        input.left();
        assert_eq!(input.rows(12).cursor, (0, 2));
        input.right();
        assert_eq!(input.rows(12).cursor, (0, 3));
    }

    #[test]
    fn rows_keep_joined_emoji_and_combining_marks_together() {
        let mut input = Input::default();
        input.paste("abcd👨‍👩‍👧‍👦e\u{301}");
        let rows = input.rows(8);
        assert_eq!(rows.lines, ["❯ abcd👨‍👩‍👧‍👦", "  e\u{301}"]);
        assert_eq!(rows.cursor, (1, 3));
        input.left();
        assert_eq!(input.rows(8).cursor, (1, 2));
    }
}
