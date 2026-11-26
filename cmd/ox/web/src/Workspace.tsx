import { Match, Show, Switch, untrack } from "solid-js";
import { ChangesList, createListing } from "./Changes";
import { FileTree } from "./FileTree";
import type { SessionControl } from "./ox";
import { type Panels, PanelToggle, type RightMode, Sash } from "./Panels";
import type { FileRoot } from "./protocol";
import { type Chat, TabArea } from "./TabArea";
import { focusedTab, openTab, reveal } from "./tabs";

// Workspace shows a selection's tab area with the panel at the right: the file
// tree or the changes. It is keyed by its selection, so its listing, which
// feeds the Changes tab's counts, the Changes view, and the tree's colors, is
// always that root's.
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
        <aside
          class={["files", { drawer: narrow() }]}
          aria-label={props.panels.rightMode() === "files" ? "Files" : "Changes"}
        >
          <header>
            <fieldset aria-label="Panel" class="toggle-group">
              <ModeButton panels={props.panels} mode="files" label="Files" />
              <ModeButton panels={props.panels} mode="changes" label="Changes" />
            </fieldset>
            <PanelToggle panels={props.panels} />
          </header>
          <Switch>
            <Match when={props.panels.rightMode() === "files"}>
              <FileTree
                root={props.root}
                control={props.control}
                listing={listing}
                current={current()}
                draggable={!narrow()}
                onOpen={openFile}
              />
            </Match>
            <Match when={props.panels.rightMode() === "changes"}>
              <ChangesList listing={listing} onOpen={openChanges} />
            </Match>
          </Switch>
        </aside>
      </Show>
    </>
  );
}

// ModeButton lists the panel's files or changes, one of two toggles.
function ModeButton(props: { panels: Panels; mode: RightMode; label: string }) {
  return (
    <button
      type="button"
      aria-pressed={props.panels.rightMode() === props.mode ? "true" : "false"}
      onClick={() => props.panels.setRightMode(props.mode)}
    >
      {props.label}
    </button>
  );
}
