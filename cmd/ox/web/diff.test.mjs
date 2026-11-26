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
  assert.deepEqual(Object.fromEntries(all.files), {
    "new/a.txt": "added",
    "new/b.txt": "added",
    "mixed/a.txt": "added",
    "mixed/b.txt": "deleted",
    "gone/deep/c.txt": "deleted",
    "top/mid/low/d.txt": "modified",
  });
  assert.deepEqual(Object.fromEntries(all.folders), {
    new: "added",
    mixed: "modified",
    "gone/deep": "deleted",
    gone: "deleted",
    "top/mid/low": "modified",
    "top/mid": "modified",
    top: "modified",
  });
});

test("statuses keeps a file apart from the folder it replaced", () => {
  const all = statuses([
    { path: "sub", status: "added", hunks: [] },
    { path: "sub/file.txt", status: "deleted", hunks: [] },
  ]);
  assert.equal(all.files.get("sub"), "added");
  assert.equal(all.folders.get("sub"), "deleted");
});
