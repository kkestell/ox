import assert from "node:assert/strict";
import { test } from "node:test";
import { highlight } from "./src/highlight.ts";

// text is what the page shows for html: its tags stripped and its character
// references decoded.
function text(html) {
  const named = { amp: "&", lt: "<", gt: ">", quot: '"' };
  return html
    .replace(/<[^>]*>/g, "")
    .replace(/&(#\d+|[a-z]+);/g, (_, ref) =>
      ref.startsWith("#") ? String.fromCharCode(Number(ref.slice(1))) : named[ref],
    );
}

test("files of a registered language come back as tokens", () => {
  for (const [path, source] of [
    ["src/main.go", "package main\n\n// note\nfunc main() {}\n"],
    ["src/View.TSX", "const v = <div />;\n"],
    ["Dockerfile", "FROM scratch\n"],
    ["build/Makefile", "all:\n\tgo build\n"],
  ]) {
    const styles = new Set([...highlight(path, source).matchAll(/<span style="([^"]*)"/g)].map((m) => m[1]));
    assert.ok(styles.size >= 2, `${path} came back with ${styles.size} token styles`);
  }
});

test("files of no registered language come back as escaped plain text", () => {
  for (const path of ["notes.txt", ".bashrc", ".sh"]) {
    const html = highlight(path, "a < b && c\r\n");
    assert.doesNotMatch(html, /<span/, path);
    assert.equal(html, "a &lt; b &amp;&amp; c&#13;\n", path);
  }
});

test("highlighting keeps the file's text", () => {
  const sources = {
    "template.ts": "const s = `a`;\r\nlet c = 1 < 2 && 3;\rlet d = 2;\n",
    "main.go": "package main\r\n\r\n// note & <tag>\r\nfunc main() {}\r\n",
    "notes.txt": "plain < text & more\r\nline two\n",
  };
  for (const [path, source] of Object.entries(sources)) {
    assert.equal(text(highlight(path, source)), source, path);
  }
});

test("a final carriage return with no newline after it is written once", () => {
  assert.equal(highlight("a.go", "last\r").split("&#13;").length - 1, 1);
});
