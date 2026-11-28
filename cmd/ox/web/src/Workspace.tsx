import { Show, untrack } from "solid-js";
import { createListing } from "./Changes";
import { FileTree } from "./FileTree";
import { type Panels, PanelToggle, Sash } from "./Panels";
import type { FileRoot } from "./protocol";
import type { SessionControl } from "./sessionControl";
import { type Chat, TabArea } from "./TabArea";
import { focusedTab, openTab, reveal } from "./tabs";

// Workspace shows a selection's tab area with the file tree in the panel at
// the right. It is keyed by its selection, so its listing, which feeds the
// Changes tab's counts, the Changes view, and the tree's top row, counts,
// colors, and deleted rows, is always that root's.
export function Workspace(props: {
  root: FileRoot;
  control: SessionControl;
  panels: Panels;
  wrap: boolean;
  onWrap(wrap: boolean): void;
  chat: Chat;
}) {
  const listing = createListing(untrack(() => props.root));
  const narrow = () => props.panels.narrow();
  // The tree marks the file the focused tab shows.
  const current = () => {
    const tab = focusedTab(props.control.layout());
    return tab.kind === "file" ? tab.path : undefined;
  };
  // Opening something from the panel closes the narrow layout's drawer over
  // it.
  const openFile = (path: string) => {
    props.control.edit((layout) => openTab(layout, { kind: "file", path }));
    props.panels.dismiss();
  };
  const openChanges = (path?: string) => {
    props.control.edit((layout) => reveal(layout, path));
    props.panels.dismiss();
  };
  return (
    <>
      <TabArea
        control={props.control}
        root={props.root}
        listing={listing}
        panels={props.panels}
        wrap={props.wrap}
        onWrap={props.onWrap}
        chat={props.chat}
      />
      <Show when={props.panels.right()}>
        <Show when={!narrow()}>
          <Sash label="Resize panel" drag={(_, after) => (dx) => props.panels.setRightWidth(after - dx)} />
        </Show>
        <aside class={["files", { drawer: narrow() }]} aria-label="Files">
          <header>
            <h2>Files</h2>
            <PanelToggle panels={props.panels} />
          </header>
          <FileTree
            root={props.root}
            control={props.control}
            listing={listing}
            current={current()}
            draggable={!narrow()}
            onOpen={openFile}
            onOpenChanges={openChanges}
          />
        </aside>
      </Show>
    </>
  );
}
