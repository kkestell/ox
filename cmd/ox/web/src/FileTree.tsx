import { createEffect, createMemo, createSignal, For, Match, onSettled, Show, Switch, untrack } from "solid-js";
import { RpcError } from "./connection";
import { type SessionControl, useOx, workspaceName } from "./ox";
import type { FolderEntry } from "./protocol";

type Tree = {
  workspaceId: string;
  // clock advances once a second while the page is visible and connected.
  clock: () => number;
  control: () => SessionControl;
};

// A folder's latest listing is its entries or the error oxd answered.
type Listing = { kind: "entries"; entries: FolderEntry[] } | { kind: "error"; message: string };

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
const byFolderThenName = (a: FolderEntry, b: FolderEntry) =>
  Number(b.folder) - Number(a.folder) || collator.compare(a.name, b.name);

// FileTree shows a workspace's directory. Each expanded folder lists itself
// when it appears and on each tick of the tree's clock, so the page asks only
// for the folders it shows.
export function FileTree(props: { workspace: { id: string; path: string }; control: SessionControl }) {
  const ox = useOx();
  const [clock, setClock] = createSignal(0);
  // The tree is keyed by its workspace, so the workspace ID never changes.
  const tree: Tree = { workspaceId: untrack(() => props.workspace.id), clock, control: () => props.control };
  onSettled(() => {
    const interval = setInterval(() => {
      if (document.visibilityState === "visible" && untrack(ox.ready)) setClock((n) => n + 1);
    }, 1000);
    return () => clearInterval(interval);
  });
  return (
    <aside class="files" aria-label="Files">
      <p>{workspaceName(props.workspace.path)}</p>
      <Folder tree={tree} path="" depth={0} />
    </aside>
  );
}

// Folder lists the entries of an expanded folder. Only oxd's answers replace
// the listing; a failure of the connection keeps the last one, since the page
// already reports the connection. One request is in flight at a time, so
// replies apply in the order sent.
function Folder(props: { tree: Tree; path: string; depth: number }) {
  const ox = useOx();
  const [listing, setListing] = createSignal<Listing>();
  let inFlight = false;
  const list = (folder: { workspaceId: string; path: string }) => {
    if (inFlight) return;
    inFlight = true;
    ox.listFolder(folder.workspaceId, folder.path)
      .then(
        ({ entries }) => setListing({ kind: "entries", entries }),
        (e: Error) => {
          if (e instanceof RpcError) setListing({ kind: "error", message: e.message });
        },
      )
      .finally(() => {
        inFlight = false;
      });
  };
  createEffect(() => {
    props.tree.clock();
    return { workspaceId: props.tree.workspaceId, path: props.path };
  }, list);
  const error = () => {
    const current = listing();
    return current?.kind === "error" ? current.message : undefined;
  };
  const entries = createMemo(() => {
    const current = listing();
    return current?.kind === "entries" ? current.entries.toSorted(byFolderThenName) : undefined;
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
                    <li class="row" style={{ "--depth": props.depth }} title={join(entry().name)}>
                      <span class="icon file" />
                      <span class="name">{entry().name}</span>
                    </li>
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

function Note(props: { depth: number; text: string }) {
  return (
    <p class="row note" style={{ "--depth": props.depth }}>
      {props.text}
    </p>
  );
}
