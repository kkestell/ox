import type { JSX } from "@solidjs/web";
import { createMemo, For, Match, Show, Switch, untrack } from "solid-js";
import { Changes } from "./Changes";
import { type SessionControl, useOx } from "./ox";
import type { Tab } from "./Panels";
import { createPoll } from "./poll";
import type { FileRoot, FolderEntry } from "./protocol";

type Tree = {
  root: FileRoot;
  control: () => SessionControl;
  onOpen(): void;
};

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
const byFolderThenName = (a: FolderEntry, b: FolderEntry) =>
  Number(b.folder) - Number(a.folder) || collator.compare(a.name, b.name);

// FileTree shows the directory beneath root, under the Files tab, or its
// changes, under the Changes tab. Each expanded folder lists itself when it
// appears and on each tick of the page's clock, so the page asks only for the
// folders it shows.
export function FileTree(props: {
  root: FileRoot;
  control: SessionControl;
  tab: Tab;
  onTab(tab: Tab): void;
  width?: number;
  toggles?: JSX.Element;
  onOpen(): void;
}) {
  // The tree is keyed by its selection, so its root never changes.
  const tree: Tree = {
    root: untrack(() => props.root),
    control: () => props.control,
    onOpen: () => props.onOpen(),
  };
  return (
    <aside
      class="files"
      aria-label="Files"
      style={{ width: props.width === undefined ? undefined : `${props.width}px` }}
    >
      <header>
        <div role="tablist" aria-label="View">
          <TabButton tab="files" label="Files" selected={props.tab} onTab={(tab) => props.onTab(tab)} />
          <TabButton tab="changes" label="Changes" selected={props.tab} onTab={(tab) => props.onTab(tab)} />
        </div>
        {props.toggles}
      </header>
      <Show
        when={props.tab === "changes"}
        fallback={
          <div role="tabpanel" aria-label="Files" class="tree">
            <Folder tree={tree} path="" depth={0} />
          </div>
        }
      >
        <Changes root={tree.root} control={props.control} onOpen={() => props.onOpen()} />
      </Show>
    </aside>
  );
}

function TabButton(props: { tab: Tab; label: string; selected: Tab; onTab(tab: Tab): void }) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={props.selected === props.tab ? "true" : "false"}
      onClick={() => props.onTab(props.tab)}
    >
      {props.label}
    </button>
  );
}

// Folder lists the entries of an expanded folder.
function Folder(props: { tree: Tree; path: string; depth: number }) {
  const ox = useOx();
  const listing = createPoll(() => ox.listFolder(props.tree.root, props.path));
  const error = () => {
    const current = listing();
    return current?.kind === "error" ? current.message : undefined;
  };
  const entries = createMemo(() => {
    const current = listing();
    return current?.kind === "value" ? current.value.entries.toSorted(byFolderThenName) : undefined;
  });
  const join = (name: string) => (props.path ? `${props.path}/${name}` : name);
  return (
    <Switch>
      <Match when={error()}>{(message) => <Note depth={props.depth} text={message()} />}</Match>
      <Match when={entries()?.length === 0}>
        <Note depth={props.depth} text="Empty" />
      </Match>
      <Match when={entries()}>
        {(list) => (
          <ul>
            <For each={list()} keyed={(entry) => entry.name}>
              {(entry) => (
                <Show
                  when={entry().folder}
                  fallback={
                    <FileRow tree={props.tree} path={join(entry().name)} name={entry().name} depth={props.depth} />
                  }
                >
                  <FolderRow tree={props.tree} path={join(entry().name)} name={entry().name} depth={props.depth} />
                </Show>
              )}
            </For>
          </ul>
        )}
      </Match>
    </Switch>
  );
}

function FolderRow(props: { tree: Tree; path: string; name: string; depth: number }) {
  const open = () => props.tree.control().isExpanded(props.path);
  return (
    <li>
      <button
        type="button"
        class="row"
        style={{ "--depth": props.depth }}
        title={props.path}
        aria-expanded={open() ? "true" : "false"}
        onClick={() => props.tree.control().setExpanded(props.path, !open())}
      >
        <span class={["icon", "chevron-right", { open: open() }]} />
        <span class={["icon", open() ? "folder-open" : "folder"]} />
        <span class="name">{props.name}</span>
      </button>
      <Show when={open()}>
        <Folder tree={props.tree} path={props.path} depth={props.depth + 1} />
      </Show>
    </li>
  );
}

function FileRow(props: { tree: Tree; path: string; name: string; depth: number }) {
  const open = () => props.tree.control().file()?.path === props.path;
  return (
    <li>
      <button
        type="button"
        class="row file-row"
        style={{ "--depth": props.depth }}
        title={props.path}
        aria-current={open() ? "true" : undefined}
        onClick={() => {
          props.tree.control().setFile(props.path);
          props.tree.onOpen();
        }}
      >
        <span class="icon file" />
        <span class="name">{props.name}</span>
      </button>
    </li>
  );
}

function Note(props: { depth: number; text: string }) {
  return (
    <p class="row note" style={{ "--depth": props.depth }}>
      {props.text}
    </p>
  );
}
