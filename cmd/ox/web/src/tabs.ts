// The center's tabs and groups as one value. Every operation returns a new
// layout that keeps these facts: a tab is in one group, a group is never
// empty, the chat tab is never closed, and the focused group exists.

export type FileMode = "source" | "preview";

export type Tab =
  | { kind: "chat" }
  | { kind: "changes"; reveal?: { path: string } }
  | { kind: "file"; path: string; mode?: FileMode };

export type Group = { id: number; tabs: Tab[]; active: number };

type Direction = "row" | "column";

export type Branch =
  | { kind: "group"; group: Group }
  | { kind: "split"; direction: Direction; children: Branch[]; sizes: number[] };

export type Layout = { root: Branch; focused: number };

type Zone = "left" | "right" | "top" | "bottom" | "center";

// A target of a drop: a position among a group's tabs, or an edge or the
// middle of a group.
export type Target = { kind: "tabs"; group: number; index?: number } | { kind: "zone"; group: number; zone: Zone };

export const tabKey = (tab: Tab) => (tab.kind === "file" ? `file:${tab.path}` : tab.kind);

export const initial = (): Layout => ({
  root: { kind: "group", group: { id: 1, tabs: [{ kind: "chat" }], active: 0 } },
  focused: 1,
});

export function groups(node: Branch): Group[] {
  return node.kind === "group" ? [node.group] : node.children.flatMap(groups);
}

// columns is how many groups of the minimum width the node needs side by side:
// one for a group, the sum of its children's for a row, the widest child's for
// a column.
export function columns(node: Branch): number {
  if (node.kind === "group") return 1;
  const each = node.children.map(columns);
  return node.direction === "row" ? each.reduce((a, b) => a + b, 0) : Math.max(...each);
}

// fits is whether the layout that dropping tab on target produces needs no
// more than width, with every group at least min wide.
export const fits = (layout: Layout, tab: Tab, target: Target, width: number, min: number) =>
  columns(placeTab(layout, tab, target).root) * min <= width;

export const findGroup = (layout: Layout, id: number) => groups(layout.root).find((g) => g.id === id);

export const findTab = (layout: Layout, key: string) =>
  groups(layout.root)
    .flatMap((g) => g.tabs)
    .find((t) => tabKey(t) === key);

const locate = (layout: Layout, key: string) => groups(layout.root).find((g) => g.tabs.some((t) => tabKey(t) === key));

// mapGroup rewrites the group with id, sharing every node it leaves alone.
function mapGroup(node: Branch, id: number, change: (group: Group) => Group): Branch {
  if (node.kind === "group") return node.group.id === id ? { kind: "group", group: change(node.group) } : node;
  let changed = false;
  const children = node.children.map((child) => {
    const next = mapGroup(child, id, change);
    if (next !== child) changed = true;
    return next;
  });
  return changed ? { ...node, children } : node;
}

const scale = (sizes: number[]) => {
  const total = sizes.reduce((a, b) => a + b, 0);
  return sizes.map((s) => s / total);
};

// tidy removes empty groups, replaces a split of one child with the child, and
// joins a split into a parent split of its direction.
function tidy(node: Branch | undefined): Branch | undefined {
  if (!node || node.kind === "group") return node;
  const children: Branch[] = [];
  const sizes: number[] = [];
  node.children.forEach((child, i) => {
    const next = tidy(child);
    if (!next) return;
    if (next.kind === "split" && next.direction === node.direction) {
      next.children.forEach((c, j) => {
        children.push(c);
        sizes.push(node.sizes[i] * next.sizes[j]);
      });
    } else {
      children.push(next);
      sizes.push(node.sizes[i]);
    }
  });
  if (children.length === 0) return undefined;
  if (children.length === 1) return children[0];
  return { ...node, children, sizes: scale(sizes) };
}

// withoutTab removes the tab with key, and its group when it was the last.
function withoutTab(layout: Layout, key: string): Layout {
  const group = locate(layout, key);
  if (!group) return layout;
  const at = group.tabs.findIndex((t) => tabKey(t) === key);
  const tabs = group.tabs.filter((_, i) => i !== at);
  const active = at < group.active ? group.active - 1 : Math.min(group.active, tabs.length - 1);
  const root =
    tabs.length === 0
      ? tidy(removeGroup(layout.root, group.id))
      : mapGroup(layout.root, group.id, (g) => ({ ...g, tabs, active }));
  if (!root) return layout;
  const focused = groups(root).some((g) => g.id === layout.focused) ? layout.focused : groups(root)[0].id;
  return { root, focused };
}

function removeGroup(node: Branch, id: number): Branch | undefined {
  if (node.kind === "group") return node.group.id === id ? undefined : node;
  const children: Branch[] = [];
  const sizes: number[] = [];
  node.children.forEach((child, i) => {
    const next = removeGroup(child, id);
    if (next) {
      children.push(next);
      sizes.push(node.sizes[i]);
    }
  });
  return { ...node, children, sizes: scale(sizes) };
}

// closeTab closes the tab with key, except the chat tab.
export function closeTab(layout: Layout, key: string): Layout {
  return key === "chat" ? layout : withoutTab(layout, key);
}

// focus makes group the focused one.
export const focus = (layout: Layout, group: number): Layout =>
  layout.focused === group || !findGroup(layout, group) ? layout : { ...layout, focused: group };

// activate shows the tab with key and focuses its group.
export function activate(layout: Layout, key: string): Layout {
  const group = locate(layout, key);
  if (!group) return layout;
  const active = group.tabs.findIndex((t) => tabKey(t) === key);
  const root = group.active === active ? layout.root : mapGroup(layout.root, group.id, (g) => ({ ...g, active }));
  return focus({ ...layout, root }, group.id);
}

// openTab shows tab where it is, or appends it to the focused group.
export function openTab(layout: Layout, tab: Tab): Layout {
  if (locate(layout, tabKey(tab))) return activate(layout, tabKey(tab));
  return placeTab(layout, tab, { kind: "tabs", group: layout.focused });
}

// insert puts tab into the group at index, active and focused.
function insert(layout: Layout, tab: Tab, groupId: number, index?: number): Layout {
  const root = mapGroup(layout.root, groupId, (g) => {
    const at = Math.min(index ?? g.tabs.length, g.tabs.length);
    return { ...g, tabs: [...g.tabs.slice(0, at), tab, ...g.tabs.slice(at)], active: at };
  });
  return { root, focused: groupId };
}

// splitAt puts tab in a new group beside or above or below the group with id.
function splitAt(layout: Layout, tab: Tab, groupId: number, zone: Exclude<Zone, "center">): Layout {
  const direction: Direction = zone === "left" || zone === "right" ? "row" : "column";
  const before = zone === "left" || zone === "top";
  const id = Math.max(...groups(layout.root).map((g) => g.id)) + 1;
  const created: Branch = { kind: "group", group: { id, tabs: [tab], active: 0 } };
  const split = (node: Branch): Branch => {
    if (node.kind === "group") {
      if (node.group.id !== groupId) return node;
      return { kind: "split", direction, children: before ? [created, node] : [node, created], sizes: [0.5, 0.5] };
    }
    const index = node.children.findIndex((c) => c.kind === "group" && c.group.id === groupId);
    if (index >= 0 && node.direction === direction) {
      const at = before ? index : index + 1;
      const half = node.sizes[index] / 2;
      const sizes = [...node.sizes];
      sizes[index] = half;
      sizes.splice(at, 0, half);
      const children = [...node.children];
      children.splice(at, 0, created);
      return { ...node, children, sizes };
    }
    return { ...node, children: node.children.map(split) };
  };
  return { root: split(layout.root), focused: id };
}

// placeTab moves tab, or opens it when no group has it, to target. A tab
// dropped on the edge of a group of which it is the only tab stays.
export function placeTab(layout: Layout, tab: Tab, target: Target): Layout {
  const key = tabKey(tab);
  const source = locate(layout, key);
  if (source && target.kind === "zone" && source.id === target.group && source.tabs.length === 1) {
    return focus(layout, source.id);
  }
  let index = target.kind === "tabs" ? target.index : undefined;
  if (index !== undefined && source?.id === target.group && source.tabs.findIndex((t) => tabKey(t) === key) < index) {
    index--;
  }
  const current = source ? (findTab(layout, key) ?? tab) : tab;
  const rest = withoutTab(layout, key);
  if (!findGroup(rest, target.group)) return layout;
  if (target.kind === "tabs" || target.zone === "center") {
    return insert(rest, current, target.group, target.kind === "tabs" ? index : undefined);
  }
  return splitAt(rest, current, target.group, target.zone);
}

// mapTab rewrites the tab with key, leaving the layout unchanged when there is none.
function mapTab(layout: Layout, key: string, change: (tab: Tab) => Tab): Layout {
  const group = locate(layout, key);
  if (!group) return layout;
  return {
    ...layout,
    root: mapGroup(layout.root, group.id, (g) => ({
      ...g,
      tabs: g.tabs.map((t) => (tabKey(t) === key ? change(t) : t)),
    })),
  };
}

// setMode shows the file at path in mode.
export const setMode = (layout: Layout, path: string, mode: FileMode): Layout =>
  mapTab(layout, `file:${path}`, (t) => (t.kind === "file" ? { ...t, mode } : t));

// reveal opens the Changes tab and asks it to scroll to the file at path.
export function reveal(layout: Layout, path?: string): Layout {
  const opened = openTab(layout, { kind: "changes" });
  if (path === undefined) return opened;
  return mapTab(opened, "changes", () => ({ kind: "changes", reveal: { path } }));
}

// setSizes sets the sizes of the split at path, the child indices from the root.
export function setSizes(layout: Layout, path: number[], sizes: number[]): Layout {
  const set = (node: Branch, rest: number[]): Branch => {
    if (node.kind === "group") return node;
    if (rest.length === 0) return { ...node, sizes };
    const [at, ...next] = rest;
    return { ...node, children: node.children.map((c, i) => (i === at ? set(c, next) : c)) };
  };
  return { ...layout, root: set(layout.root, path) };
}

// leftmostTop is the group at the top left, and rightmostTop the one at the
// top right.
export function leftmostTop(layout: Layout): number {
  let node = layout.root;
  while (node.kind === "split") node = node.children[0];
  return node.group.id;
}

export function rightmostTop(layout: Layout): number {
  let node = layout.root;
  while (node.kind === "split")
    node = node.direction === "row" ? node.children[node.children.length - 1] : node.children[0];
  return node.group.id;
}

// dropZone is where a pointer at x, y falls in rect: an edge when it is in the
// edge's outer quarter, the nearer edge in a corner, else the middle.
export function dropZone(
  rect: { left: number; top: number; width: number; height: number },
  x: number,
  y: number,
): Zone {
  const fx = (x - rect.left) / rect.width;
  const fy = (y - rect.top) / rect.height;
  const dx = Math.min(fx, 1 - fx);
  const dy = Math.min(fy, 1 - fy);
  if (dx >= 0.25 && dy >= 0.25) return "center";
  if (dx <= dy) return fx < 0.5 ? "left" : "right";
  return fy < 0.5 ? "top" : "bottom";
}

// focusedGroup is the focused group, which the layout keeps in existence.
export const focusedGroup = (layout: Layout) => findGroup(layout, layout.focused) as Group;

// focusedTab is the tab the focused group shows.
export function focusedTab(layout: Layout): Tab {
  const group = focusedGroup(layout);
  return group.tabs[group.active];
}
