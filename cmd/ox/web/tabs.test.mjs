import assert from "node:assert/strict";
import { test } from "node:test";
import {
  activate,
  closeTab,
  dropZone,
  groups,
  initial,
  leftmostTop,
  openTab,
  placeTab,
  rightmostTop,
  setMode,
  tabKey,
} from "./src/tabs.ts";

const file = (path) => ({ kind: "file", path });
const keys = (layout) => groups(layout.root).map((g) => g.tabs.map(tabKey));
const open = (layout, ...paths) => paths.reduce((l, p) => openTab(l, file(p)), layout);

test("openTab appends to the focused group and activates the tab; opening it again only focuses it", () => {
  let layout = open(initial(), "a", "b");
  assert.deepEqual(keys(layout), [["chat", "file:a", "file:b"]]);
  assert.equal(groups(layout.root)[0].active, 2);
  layout = activate(layout, "file:a");
  layout = openTab(layout, file("a"));
  assert.deepEqual(keys(layout), [["chat", "file:a", "file:b"]]);
  assert.equal(groups(layout.root)[0].active, 1);
});

test("closeTab removes a tab, picks a neighbor, and never closes the chat", () => {
  let layout = open(initial(), "a", "b");
  layout = closeTab(layout, "file:b");
  assert.equal(groups(layout.root)[0].active, 1);
  assert.deepEqual(keys(closeTab(layout, "chat")), [["chat", "file:a"]]);
});

test("an edge drop splits the group and puts the tab in the new one", () => {
  const layout = placeTab(open(initial(), "a"), file("a"), { kind: "zone", group: 1, zone: "right" });
  assert.deepEqual(keys(layout), [["chat"], ["file:a"]]);
  assert.equal(layout.root.direction, "row");
  assert.deepEqual(layout.root.sizes, [0.5, 0.5]);
  assert.equal(layout.focused, 2);
});

test("an edge drop in the direction of the parent split joins it and halves the group's share", () => {
  let layout = placeTab(open(initial(), "a", "b"), file("a"), { kind: "zone", group: 1, zone: "right" });
  layout = placeTab(layout, file("b"), { kind: "zone", group: 2, zone: "right" });
  assert.equal(layout.root.children.length, 3);
  assert.deepEqual(layout.root.sizes, [0.5, 0.25, 0.25]);
  assert.deepEqual(keys(layout), [["chat"], ["file:a"], ["file:b"]]);
});

test("an edge drop across the parent's direction nests a split", () => {
  let layout = placeTab(open(initial(), "a", "b"), file("a"), { kind: "zone", group: 1, zone: "right" });
  layout = placeTab(layout, file("b"), { kind: "zone", group: 2, zone: "bottom" });
  assert.equal(layout.root.children[1].direction, "column");
  assert.deepEqual(keys(layout), [["chat"], ["file:a"], ["file:b"]]);
});

test("dropping a group's only tab on its own edge changes nothing", () => {
  const layout = placeTab(open(initial(), "a"), file("a"), { kind: "zone", group: 1, zone: "right" });
  const again = placeTab(layout, file("a"), { kind: "zone", group: 2, zone: "left" });
  assert.deepEqual(keys(again), [["chat"], ["file:a"]]);
});

test("moving a group's last tab removes the group and collapses the split", () => {
  let layout = placeTab(open(initial(), "a"), file("a"), { kind: "zone", group: 1, zone: "right" });
  layout = placeTab(layout, file("a"), { kind: "tabs", group: 1 });
  assert.equal(layout.root.kind, "group");
  assert.deepEqual(keys(layout), [["chat", "file:a"]]);
  assert.equal(layout.focused, 1);
});

test("closing the last tab of a group removes it and focuses a group that exists", () => {
  let layout = placeTab(open(initial(), "a"), file("a"), { kind: "zone", group: 1, zone: "right" });
  layout = closeTab(layout, "file:a");
  assert.equal(layout.root.kind, "group");
  assert.equal(layout.focused, 1);
});

test("a tab dropped among a group's tabs lands at the index, counting its own removal", () => {
  let layout = open(initial(), "a", "b", "c");
  layout = placeTab(layout, file("a"), { kind: "tabs", group: 1, index: 4 });
  assert.deepEqual(keys(layout), [["chat", "file:b", "file:c", "file:a"]]);
});

test("the chat tab can move to another group", () => {
  const layout = placeTab(open(initial(), "a"), { kind: "chat" }, { kind: "zone", group: 1, zone: "right" });
  assert.deepEqual(keys(layout), [["file:a"], ["chat"]]);
});

test("a file not yet open drops into a new group", () => {
  const layout = placeTab(initial(), file("z"), { kind: "zone", group: 1, zone: "bottom" });
  assert.deepEqual(keys(layout), [["chat"], ["file:z"]]);
  assert.equal(layout.root.direction, "column");
});

test("setMode keeps the mode on the tab through moves", () => {
  let layout = setMode(open(initial(), "a.md"), "a.md", "source");
  layout = placeTab(layout, file("a.md"), { kind: "zone", group: 1, zone: "right" });
  assert.equal(groups(layout.root)[1].tabs[0].mode, "source");
});

test("leftmostTop and rightmostTop name the groups at the top corners", () => {
  let layout = placeTab(open(initial(), "a", "b"), file("a"), { kind: "zone", group: 1, zone: "right" });
  layout = placeTab(layout, file("b"), { kind: "zone", group: 2, zone: "bottom" });
  assert.equal(leftmostTop(layout), 1);
  assert.equal(rightmostTop(layout), 2);
  layout = placeTab(layout, file("a"), { kind: "zone", group: 1, zone: "top" });
  assert.equal(leftmostTop(layout), 4);
  assert.equal(rightmostTop(layout), 3);
});

test("dropZone gives an edge in its outer quarter, the nearer edge in a corner, else the middle", () => {
  const rect = { left: 0, top: 0, width: 100, height: 100 };
  assert.equal(dropZone(rect, 50, 50), "center");
  assert.equal(dropZone(rect, 10, 50), "left");
  assert.equal(dropZone(rect, 90, 50), "right");
  assert.equal(dropZone(rect, 50, 10), "top");
  assert.equal(dropZone(rect, 50, 90), "bottom");
  assert.equal(dropZone(rect, 5, 20), "left");
  assert.equal(dropZone(rect, 20, 5), "top");
  assert.equal(dropZone(rect, 24, 50), "left");
  assert.equal(dropZone(rect, 26, 50), "center");
});
