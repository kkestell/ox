/** Markdown `text` with its code spans shown as code. */
export function CodeSpans({ text }: { text: string }) {
  return codeSpans(text).map((part, index) =>
    part.code ? (
      // biome-ignore lint/suspicious/noArrayIndexKey: the parts are replaced whole with the text
      <code key={index}>{part.text}</code>
    ) : (
      part.text
    ),
  );
}

/**
 * Splits Markdown `text` into plain text, with its backslash escapes
 * resolved, and the text of its code spans.
 */
function codeSpans(text: string): { code: boolean; text: string }[] {
  const parts: { code: boolean; text: string }[] = [];
  let plain = "";
  let i = 0;
  while (i < text.length) {
    if (text[i] === "\\" && asciiPunctuation.test(text[i + 1] ?? "")) {
      plain += text[i + 1];
      i += 2;
      continue;
    }
    if (text[i] !== "`") {
      plain += text[i];
      i += 1;
      continue;
    }
    let length = 0;
    while (text[i + length] === "`") length += 1;
    const fence = "`".repeat(length);
    // The span closes at the next run of exactly as many backticks.
    let close = text.indexOf(fence, i + length);
    while (close !== -1 && (text[close - 1] === "`" || text[close + length] === "`")) {
      close = text.indexOf(fence, close + 1);
    }
    if (close === -1) {
      plain += fence;
      i += length;
      continue;
    }
    let code = text.slice(i + length, close).replaceAll("\n", " ");
    if (code.startsWith(" ") && code.endsWith(" ") && /[^ ]/.test(code)) {
      code = code.slice(1, -1);
    }
    if (plain) parts.push({ code: false, text: plain });
    parts.push({ code: true, text: code });
    plain = "";
    i = close + length;
  }
  if (plain) parts.push({ code: false, text: plain });
  return parts;
}

// The characters a backslash escapes in Markdown.
const asciiPunctuation = /^[!-/:-@[-`{-~]$/;
