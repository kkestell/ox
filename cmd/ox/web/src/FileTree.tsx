import type { JSX } from "@solidjs/web";
import { createMemo, For, Match, Show, Switch, untrack } from "solid-js";
import { type SessionControl, useOx, workspaceName } from "./ox";
import { createPoll } from "./poll";
import type { FolderEntry } from "./protocol";

type Tree = {
  workspaceId: string;
  control: () => SessionControl;
};

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
const byFolderThenName = (a: FolderEntry, b: FolderEntry) =>
  Number(b.folder) - Number(a.folder) || collator.compare(a.name, b.name);

// FileTree shows a workspace's directory. Each expanded folder lists itself
// when it appears and on each tick of the page's clock, so the page asks only
// for the folders it shows.
export function FileTree(props: {
  workspace: { id: string; path: string };
  control: SessionControl;
  width: number;
  toggles: JSX.Element;
}) {
  // The tree is keyed by its workspace, so the workspace ID never changes.
  const tree: Tree = { workspaceId: untrack(() => props.workspace.id), control: () => props.control };
  return (
    <aside class="files" aria-label="Files" style={{ width: `${props.width}px` }}>
      <header>
        <p>{workspaceName(props.workspace.path)}</p>
        {props.toggles}
      </header>
      <div class="tree">
        <Folder tree={tree} path="" depth={0} />
      </div>
    </aside>
  );
}

// Folder lists the entries of an expanded folder.
function Folder(props: { tree: Tree; path: string; depth: number }) {
  const ox = useOx();
  const listing = createPoll(() => ox.listFolder(props.tree.workspaceId, props.path));
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
  const open = () => props.tree.control().file() === props.path;
  return (
    <li>
      <button
        type="button"
        class="row file-row"
        style={{ "--depth": props.depth }}
        title={props.path}
        aria-current={open() ? "true" : undefined}
        onClick={() => props.tree.control().setFile(props.path)}
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
