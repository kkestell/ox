import assert from "node:assert/strict";
import { test } from "node:test";
import { statuses, wholeFile } from "./src/diff.ts";

test("wholeFile fills the lines before, between, and after the hunks from the text", () => {
  const text = "a\nb\nC\nd\ne\nF\ng\n";
  const hunks = [
    { old_start: 3, new_start: 3, lines: ["-c", "+C"] },
    { old_start: 6, new_start: 6, lines: ["-f", "+F"] },
  ];
  assert.deepEqual(wholeFile(text, hunks), [" a", " b", "-c", "+C", " d", " e", "-f", "+F", " g"]);
});

test("wholeFile counts only kept and added lines toward the text's lines", () => {
  const text = "a\nd\ne\n";
  const hunks = [{ old_start: 2, new_start: 2, lines: ["-b", "-c", " d"] }];
  assert.deepEqual(wholeFile(text, hunks), [" a", "-b", "-c", " d", " e"]);
});

test("wholeFile reads a text with no trailing newline", () => {
  assert.deepEqual(wholeFile("a\nB", [{ old_start: 2, new_start: 2, lines: ["-b", "+B"] }]), [" a", "-b", "+B"]);
});

test("wholeFile gives an empty text with no hunks no lines", () => {
  assert.deepEqual(wholeFile("", []), []);
});

test("wholeFile extends past the hunks with the text's remaining lines", () => {
  const text = "A\nb\nc\nd\n";
  assert.deepEqual(wholeFile(text, [{ old_start: 1, new_start: 1, lines: ["+A"] }]), ["+A", " b", " c", " d"]);
});

test("statuses gives each changed file and each folder above one a status", () => {
  const all = statuses([
    { path: "new/a.txt", status: "added", hunks: [] },
    { path: "new/b.txt", status: "added", hunks: [] },
    { path: "mixed/a.txt", status: "added", hunks: [] },
    { path: "mixed/b.txt", status: "deleted", hunks: [] },
    { path: "gone/deep/c.txt", status: "deleted", hunks: [] },
    { path: "top/mid/low/d.txt", status: "modified", hunks: [] },
  ]);
  assert.deepEqual(Object.fromEntries(all), {
    "new/a.txt": "added",
    "new/b.txt": "added",
    new: "added",
    "mixed/a.txt": "added",
    "mixed/b.txt": "deleted",
    mixed: "modified",
    "gone/deep/c.txt": "deleted",
    "gone/deep": "deleted",
    gone: "deleted",
    "top/mid/low/d.txt": "modified",
    "top/mid/low": "modified",
    "top/mid": "modified",
    top: "modified",
  });
});
