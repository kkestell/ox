import assert from "node:assert/strict";
import { test } from "node:test";
import { deletedEntries, statuses, treeEntries } from "./src/diff.ts";

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

test("deletedEntries lists each deleted file's folders and the file, once each, in the folder above", () => {
  const all = deletedEntries([
    { path: "gone/deep/c.txt", status: "deleted", hunks: [] },
    { path: "gone/deep/d.txt", status: "deleted", hunks: [] },
    { path: "gone/e.txt", status: "deleted", hunks: [] },
    { path: "top.txt", status: "deleted", hunks: [] },
    { path: "kept/a.txt", status: "modified", hunks: [] },
    { path: "new.txt", status: "added", hunks: [] },
  ]);
  assert.deepEqual(Object.fromEntries(all), {
    "": [
      { name: "gone", folder: true },
      { name: "top.txt", folder: false },
    ],
    gone: [
      { name: "deep", folder: true },
      { name: "e.txt", folder: false },
    ],
    "gone/deep": [
      { name: "c.txt", folder: false },
      { name: "d.txt", folder: false },
    ],
  });
});

test("treeEntries marks disk and deleted entries, lets the disk win a shared name, and sorts folders first", () => {
  const entries = treeEntries(
    [
      { name: "b.txt", folder: false },
      { name: "src", folder: true },
      { name: "sub", folder: false },
    ],
    [
      { name: "a.txt", folder: false },
      { name: "gone", folder: true },
      { name: "sub", folder: true },
      { name: "b.txt", folder: false },
    ],
  );
  assert.deepEqual(entries, [
    { name: "gone", folder: true, onDisk: false },
    { name: "src", folder: true, onDisk: true },
    { name: "a.txt", folder: false, onDisk: false },
    { name: "b.txt", folder: false, onDisk: true },
    { name: "sub", folder: false, onDisk: true },
  ]);
});
