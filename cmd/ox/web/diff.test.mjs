import assert from "node:assert/strict";
import { test } from "node:test";
import { statuses } from "./src/diff.ts";

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
