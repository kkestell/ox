import { Match, Show, Switch, untrack } from "solid-js";
import { ChangesView, createListing } from "./Changes";
import { FileTree } from "./FileTree";
import { FileView } from "./FileView";
import type { SessionControl } from "./ox";
import { ModeTabs, type Panels, Sash } from "./Panels";
import type { FileRoot } from "./protocol";

// Workbench shows, beside the transcript, the open file with the file tree
// beside it, or the changes, as the panels' mode says. Its parts are columns of
// the layout beside the transcript: the sash that splits the width between the
// transcript and the file or the changes, the file or the changes, and the
// sash and the file tree. Without an open file, the file tree alone stands
// beside the transcript. Its changes are listed once, for the tree's colors,
// the branch summary, the file's changes, and the changes view. It is keyed by
// its selection, so its listing is always that root's.
export function Workbench(props: {
  root: FileRoot;
  control: SessionControl;
  panels: Panels;
  wrap: boolean;
  onWrap(wrap: boolean): void;
}) {
  const listing = createListing(untrack(() => props.root));
  const file = () => props.control.file();
  const path = () => file()?.path;
  const narrow = () => props.panels.narrow();
  const open = (path: string) => {
    props.control.openFile(path);
    props.panels.setMode("files");
  };
  const Close = () => (
    <button
      type="button"
      class="icon panel-right"
      aria-label="Toggle workbench"
      title="Toggle workbench"
      aria-pressed="true"
      onClick={() => props.panels.setMode(undefined)}
    />
  );
  // The narrow layout hides the transcript's header, so the workbench's own
  // header carries the tabs.
  const Tabs = () => (
    <Show when={narrow()}>
      <ModeTabs panels={props.panels} />
    </Show>
  );
  const ResizeTree = () => (
    <Show when={!narrow()}>
      <Sash label="Resize files" drag={(_, after) => (dx) => props.panels.setRightWidth(after - dx)} />
    </Show>
  );
  const Tree = (each: { alone: boolean }) => (
    <FileTree
      root={props.root}
      control={props.control}
      listing={listing}
      current={path()}
      drawer={narrow() && !each.alone}
      start={each.alone ? <Close /> : undefined}
      end={each.alone ? <Tabs /> : undefined}
      onOpen={open}
    />
  );
  // The transcript and the file or the changes share their width in the wide
  // layout.
  const shared = () => !narrow() && (props.panels.mode() === "changes" || path() !== undefined);
  return (
    <>
      <Show when={shared()}>
        <Sash
          label="Resize file viewer"
          drag={(before, after) => {
            const control = props.control;
            return (dx) => control.setSplit((before + dx) / (before + after));
          }}
        />
      </Show>
      <Switch>
        <Match when={props.panels.mode() === "changes"}>
          <ChangesView listing={listing} start={<Close />} end={<Tabs />} onOpen={open} />
        </Match>
        <Match when={path()}>
          {/* Keyed by path, so a file's view shows only that file. */}
          <Show when={path()} keyed>
            {(shown) => (
              <FileView
                root={props.root}
                path={shown}
                change={listing.change(shown)}
                mode={file()?.mode}
                onMode={(mode) => props.control.setMode(shown, mode)}
                wrap={props.wrap}
                onWrap={(wrap) => props.onWrap(wrap)}
                onClose={() => props.control.closeFile()}
                start={<Close />}
                end={
                  <Show when={narrow()}>
                    <button
                      type="button"
                      class="icon folder-tree"
                      aria-label="Toggle file tree"
                      title="Toggle file tree"
                      aria-pressed={props.panels.treeDrawer() ? "true" : "false"}
                      onClick={() => props.panels.toggleTree()}
                    />
                    <ModeTabs panels={props.panels} />
                  </Show>
                }
              />
            )}
          </Show>
          <Show when={!narrow() || props.panels.treeDrawer()}>
            <ResizeTree />
            <Tree alone={false} />
          </Show>
        </Match>
        <Match when={props.panels.mode() === "files"}>
          <ResizeTree />
          <Tree alone={true} />
        </Match>
      </Switch>
    </>
  );
}
