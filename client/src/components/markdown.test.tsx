import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { Markdown } from "./markdown.tsx";

function render(text: string): string {
  return renderToStaticMarkup(<Markdown text={text} />);
}

test("renders GitHub-flavored Markdown and streamed code", () => {
  const html = render([
    "## Heading",
    "",
    "Some **bold** and `inline` text.",
    "",
    "- one",
    "- two",
    "",
    "| a | b |",
    "| - | - |",
    "| 1 | 2 |",
    "",
    "~~gone~~",
    "",
    "```go",
    "func main() {",
  ].join("\n"));

  expect(html).toContain("<h2>Heading</h2>");
  expect(html).toContain("<strong>bold</strong>");
  expect(html).toContain("<code>inline</code>");
  expect(html).toContain("<li>one</li>");
  expect(html).toContain("<del>gone</del>");
  expect(html).toContain('<div class="max-w-full overflow-x-auto"><table');
  expect(html).toContain("func main() {");
  expect(html).toContain('max-h-72 min-w-0 max-w-full overflow-auto');
});

test("keeps a single newline as a line break", () => {
  expect(render("line one\nline two")).toContain("line one<br/>\nline two");
});

test("keeps unsafe Markdown inert", () => {
  const html = render([
    '<img src=x onerror=alert(1)>',
    "",
    "[script](javascript:alert(1))",
    "",
    "![diagram](https://example.test/diagram.png)",
  ].join("\n"));

  expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;");
  expect(html).not.toContain("<img src=x");
  expect(html).toContain("<span>script</span>");
  expect(html).not.toContain('href="javascript:');
  expect(html).toContain("<span>diagram</span>");
  expect(html).not.toContain('src="https://example.test/diagram.png"');
});
