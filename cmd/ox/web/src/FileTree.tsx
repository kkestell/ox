import { createMemo, For, Match, Show, Switch, untrack } from "solid-js";
import { Counts, type Listing } from "./Changes";
import { treeEntries } from "./diff";
import { fileDrag } from "./drag";
import { type SessionControl, useOx } from "./ox";
import { createPoll, type Polled } from "./poll";
import type { FileRoot, FolderEntry } from "./protocol";

type Tree = {
  root: FileRoot;
  control: () => SessionControl;
  listing: Listing;
  current: () => string | undefined;
  draggable: () => boolean;
  onOpen(path: string): void;
  onOpenChanges(path?: string): void;
};

// FileTree shows the directory beneath root under a row with the branch and
// the lines the changes add and remove, which opens the Changes tab. Each
// changed file and folder is colored by its status, a changed file's counts
// open the Changes tab at it, and each folder also lists the deleted files and
// folders the changes record in it. It marks current, the file the focused tab
// shows. A file's row can be dragged into the tab area unless draggable is
// off. Each expanded folder on disk lists itself when it appears and on each
// tick of the page's clock, so the page asks only for the folders it shows.
export function FileTree(props: {
  root: FileRoot;
  control: SessionControl;
  listing: Listing;
  current?: string;
  draggable: boolean;
  onOpen(path: string): void;
  onOpenChanges(path?: string): void;
}) {
  // The tree is keyed by its selection, so its root never changes.
  const tree: Tree = {
    root: untrack(() => props.root),
    control: () => props.control,
    listing: untrack(() => props.listing),
    current: () => props.current,
    draggable: () => props.draggable,
    onOpen: (path) => props.onOpen(path),
    onOpenChanges: (path) => props.onOpenChanges(path),
  };
  return (
    <div class="tree">
      <Show when={tree.listing.changes()}>
        {(current) => (
          <button type="button" class="row summary" onClick={() => tree.onOpenChanges()}>
            <Show when={current().branch}>
              {(branch) => (
                <>
                  <span class="icon git-branch" />
                  <span class="branch">{branch()}</span>
                </>
              )}
            </Show>
            <Counts added={tree.listing.total().added} removed={tree.listing.total().removed} />
          </button>
        )}
      </Show>
      <Folder tree={tree} path="" depth={0} />
    </div>
  );
}

// Folder lists the entries of an expanded folder on disk.
function Folder(props: { tree: Tree; path: string; depth: number }) {
  const ox = useOx();
  const disk = createPoll(() => ox.listFolder(props.tree.root, props.path));
  return <Entries tree={props.tree} path={props.path} depth={props.depth} disk={disk} />;
}

// Entries lists a folder's entries: its disk listing, once disk has one,
// merged with the deleted entries the changes record in it. A deleted folder
// has no disk listing, so it lists its deleted entries alone.
function Entries(props: {
  tree: Tree;
  path: string;
  depth: number;
  disk?: () => Polled<{ entries: FolderEntry[] }> | undefined;
}) {
  const error = () => {
    const current = props.disk?.();
    return current?.kind === "error" ? current.message : undefined;
  };
  const entries = createMemo(() => {
    const deleted = props.tree.listing.deletedIn(props.path);
    if (!props.disk) return treeEntries([], deleted);
    const current = props.disk();
    return current?.kind === "value" ? treeEntries(current.value.entries, deleted) : undefined;
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
                <Switch>
                  <Match when={entry().folder}>
                    <FolderRow
                      tree={props.tree}
                      path={join(entry().name)}
                      name={entry().name}
                      depth={props.depth}
                      onDisk={entry().onDisk}
                    />
                  </Match>
                  <Match when={entry().onDisk}>
                    <FileRow tree={props.tree} path={join(entry().name)} name={entry().name} depth={props.depth} />
                  </Match>
                  <Match when={true}>
                    <DeletedRow tree={props.tree} path={join(entry().name)} name={entry().name} depth={props.depth} />
                  </Match>
                </Switch>
              )}
            </For>
          </ul>
        )}
      </Match>
    </Switch>
  );
}

// FolderRow expands and collapses a folder. A folder on disk lists itself;
// a deleted one lists the deleted entries beneath it.
function FolderRow(props: { tree: Tree; path: string; name: string; depth: number; onDisk: boolean }) {
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
        <Show when={props.onDisk} fallback={<Entries tree={props.tree} path={props.path} depth={props.depth + 1} />}>
          <Folder tree={props.tree} path={props.path} depth={props.depth + 1} />
        </Show>
      </Show>
    </li>
  );
}

// FileRow opens a file on disk, and a changed file's counts open the Changes
// tab at it.
function FileRow(props: { tree: Tree; path: string; name: string; depth: number }) {
  const open = () => props.tree.current() === props.path;
  const counts = () => props.tree.listing.fileCounts(props.path);
  return (
    <li class={{ changed: counts() !== undefined }}>
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
      <Show when={counts()}>
        {(each) => (
          <button
            type="button"
            class="counts-button"
            aria-label={`Changes in ${props.name}`}
            title={`Changes in ${props.name}`}
            onClick={() => props.tree.onOpenChanges(props.path)}
          >
            <Counts added={each().added} removed={each().removed} />
          </button>
        )}
      </Show>
    </li>
  );
}

// DeletedRow shows a deleted file with its counts, and opens the Changes tab
// at it.
function DeletedRow(props: { tree: Tree; path: string; name: string; depth: number }) {
  return (
    <li>
      <button
        type="button"
        class="row file-row"
        style={{ "--depth": props.depth }}
        title={props.path}
        data-status={props.tree.listing.fileStatus(props.path)}
        onClick={() => props.tree.onOpenChanges(props.path)}
      >
        <span class="icon file" />
        <span class="name">{props.name}</span>
        <Show when={props.tree.listing.fileCounts(props.path)}>
          {(each) => <Counts added={each().added} removed={each().removed} />}
        </Show>
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
