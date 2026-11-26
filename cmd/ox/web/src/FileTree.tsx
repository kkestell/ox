import { createMemo, For, Match, Show, Switch, untrack } from "solid-js";
import type { Listing } from "./Changes";
import { fileDrag } from "./drag";
import { type SessionControl, useOx } from "./ox";
import { createPoll } from "./poll";
import type { FileRoot, FolderEntry } from "./protocol";

type Tree = {
  root: FileRoot;
  control: () => SessionControl;
  listing: Listing;
  current: () => string | undefined;
  draggable: () => boolean;
  onOpen(path: string): void;
};

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
const byFolderThenName = (a: FolderEntry, b: FolderEntry) =>
  Number(b.folder) - Number(a.folder) || collator.compare(a.name, b.name);

// FileTree shows the directory beneath root, each changed file and folder
// colored by its status, and marks current, the file the focused tab shows. A
// file's row can be dragged into the tab area unless draggable is off. Each
// expanded folder lists itself when it appears and on each tick of the page's
// clock, so the page asks only for the folders it shows.
export function FileTree(props: {
  root: FileRoot;
  control: SessionControl;
  listing: Listing;
  current?: string;
  draggable: boolean;
  onOpen(path: string): void;
}) {
  // The tree is keyed by its selection, so its root never changes.
  const tree: Tree = {
    root: untrack(() => props.root),
    control: () => props.control,
    listing: untrack(() => props.listing),
    current: () => props.current,
    draggable: () => props.draggable,
    onOpen: (path) => props.onOpen(path),
  };
  return (
    <div class="tree">
      <Folder tree={tree} path="" depth={0} />
    </div>
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
        data-status={props.tree.listing.folderStatus(props.path)}
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
  const open = () => props.tree.current() === props.path;
  return (
    <li>
      <button
        type="button"
        class="row file-row"
        style={{ "--depth": props.depth }}
        title={props.path}
        data-status={props.tree.listing.fileStatus(props.path)}
        aria-current={open() ? "true" : undefined}
        draggable={props.tree.draggable() ? "true" : "false"}
        onDragStart={(event) => {
          event.dataTransfer?.setData(fileDrag, props.path);
          if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
        }}
        onClick={() => props.tree.onOpen(props.path)}
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
