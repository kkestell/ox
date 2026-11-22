import { createEffect, createSignal, For, Match, onSettled, Show, Switch } from "solid-js";
import { FileTree } from "./FileTree";
import { FileView } from "./FileView";
import { createDraft, type Draft, NewSessionView } from "./NewSessionView";
import { OxProvider, useOx } from "./ox";
import { createPanels, PanelToggles, Sash } from "./Panels";
import { SessionView } from "./SessionView";
import { Sidebar } from "./Sidebar";

export type Selection = { kind: "session"; id: string } | { kind: "draft"; workspaceId: string };

// The URL's hash holds the selected session, so a reload or a hash change
// selects it.
function selectionFromHash(): Selection | undefined {
  let id: string;
  try {
    id = decodeURIComponent(location.hash.slice(1));
  } catch {
    return undefined;
  }
  return id ? { kind: "session", id } : undefined;
}

export default function App() {
  return (
    <OxProvider>
      <Layout />
    </OxProvider>
  );
}

function Layout() {
  const ox = useOx();
  const [selected, setSelected] = createSignal<Selection | undefined>(selectionFromHash());
  const [drafts, setDrafts] = createSignal<Draft[]>([]);
  const select = (selection: Selection) => {
    if (selection.kind === "draft" && !drafts().some((d) => d.workspaceId === selection.workspaceId)) {
      const draft = createDraft(selection.workspaceId, ox);
      setDrafts((all) => [...all, draft]);
    }
    setSelected(selection);
    const hash = selection.kind === "session" ? `#${encodeURIComponent(selection.id)}` : "";
    history.replaceState(null, "", location.pathname + location.search + hash);
  };
  onSettled(() => {
    const follow = () => setSelected(selectionFromHash());
    addEventListener("hashchange", follow);
    return () => removeEventListener("hashchange", follow);
  });
  const session = () => {
    const s = selected();
    if (s?.kind !== "session") return;
    return ox.session(s.id);
  };
  // A removed workspace's draft is hidden until the effect below drops it.
  const liveDrafts = () => drafts().filter((d) => ox.state.workspaces.some((w) => w.id === d.workspaceId));
  createEffect(
    () => drafts().filter((d) => !ox.state.workspaces.some((w) => w.id === d.workspaceId)),
    (removed) => {
      if (removed.length === 0) return;
      setDrafts((all) => all.filter((d) => !removed.includes(d)));
      for (const d of removed) ox.release(d.id);
    },
  );
  const draft = () => {
    const s = selected();
    return s?.kind === "draft" ? liveDrafts().find((d) => d.workspaceId === s.workspaceId) : undefined;
  };

  // The file tree shows the selected session's or draft's workspace, with
  // that session's or draft's expanded folders.
  const files = () => {
    const current = session();
    if (current) {
      const workspace = ox.workspaceOf(current.id);
      return workspace && { workspace, control: ox.control(current.id) };
    }
    const d = draft();
    const workspace = d && ox.state.workspaces.find((w) => w.id === d.workspaceId);
    return workspace && { workspace, control: d.control };
  };

  const panels = createPanels();
  // The file tree, when it shows. The toggles sit in its header then, and in
  // the session's header otherwise.
  const tree = () => (panels.right() ? files() : undefined);
  // The transcript's share of the width, while the file viewer shows beside it.
  // Alone, it fills the space.
  const share = () => {
    const current = files();
    return current?.control.file() ? current.control.split() : undefined;
  };
  const Toggles = () => (
    <Show when={!tree()}>
      <PanelToggles panels={panels} />
    </Show>
  );
  // The title row of a view that has no header of its own.
  const BareHeader = () => (
    <header class="context">
      <div class="title">
        <Toggles />
      </div>
    </header>
  );

  const finish = (created: Draft) => {
    if (draft() === created) select({ kind: "session", id: created.id });
    setDrafts((all) => all.filter((d) => d !== created));
  };

  // A creation is unconfirmed when its reply is lost, or when a retry finds
  // the session already created. Once the session is live, reattach a shown
  // draft without sending.
  createEffect(
    () => liveDrafts().find((d) => d.control.recoverable() && (d === draft() || session()?.id === d.id)),
    (recovered) => {
      if (recovered) {
        recovered.control.recover();
        finish(recovered);
      }
    },
  );

  return (
    <>
      <Sidebar selected={selected()} onSelect={select} width={panels.leftWidth()} hidden={!panels.left()} />
      <Show when={panels.left()}>
        <Sash label="Resize sessions" drag={(before) => (dx) => panels.setLeftWidth(before + dx)} />
      </Show>
      <main style={{ "flex-grow": share() }}>
        <Show when={ox.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
        <For each={liveDrafts().filter((d) => d.control.error() && d !== draft())}>
          {(failed) => (
            <p role="alert">
              {failed.control.error()}{" "}
              <button type="button" onClick={() => select({ kind: "draft", workspaceId: failed.workspaceId })}>
                Reopen draft
              </button>
            </p>
          )}
        </For>
        {/* Keyed, so each session and draft gets its own view and view state. */}
        <Switch fallback={<BareHeader />}>
          <Match when={session()} keyed>
            {(session) => (
              <Show
                when={session.load.snapshot}
                fallback={
                  <>
                    <BareHeader />
                    <p role="status">Loading session…</p>
                  </>
                }
              >
                {(snapshot) => <SessionView session={session} snapshot={snapshot()} toggles={<Toggles />} />}
              </Show>
            )}
          </Match>
          <Match when={draft()} keyed>
            {(current) => <NewSessionView draft={current} onCreated={() => finish(current)} toggles={<Toggles />} />}
          </Match>
        </Switch>
      </main>
      {/* Keyed by workspace, so sessions of one workspace share its listings,
          and by path, so a file's view shows only that file. */}
      <Show when={files()?.workspace.id} keyed>
        {(workspaceId) => (
          <Show when={files()}>
            {(current) => (
              <>
                <Show when={current().control.file()} keyed>
                  {(path) => (
                    <>
                      <Sash
                        label="Resize file viewer"
                        drag={(before, after) => {
                          const control = current().control;
                          return (dx) => control.setSplit(before + dx, before + after);
                        }}
                      />
                      <FileView
                        workspaceId={workspaceId}
                        path={path}
                        share={1 - current().control.split()}
                        onClose={() => current().control.setFile(undefined)}
                      />
                    </>
                  )}
                </Show>
                <Show when={tree()}>
                  {(shown) => (
                    <>
                      <Sash label="Resize files" drag={(_, after) => (dx) => panels.setRightWidth(after - dx)} />
                      <FileTree
                        workspace={shown().workspace}
                        control={shown().control}
                        width={panels.rightWidth()}
                        toggles={<PanelToggles panels={panels} />}
                      />
                    </>
                  )}
                </Show>
              </>
            )}
          </Show>
        )}
      </Show>
    </>
  );
}
