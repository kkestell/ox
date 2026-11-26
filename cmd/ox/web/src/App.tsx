import { createEffect, createSignal, For, Match, onSettled, Show, Switch } from "solid-js";
import { Disconnected } from "./connection";
import { createDraft, type Draft, NewSessionView } from "./NewSessionView";
import { OxProvider, type SessionControl, useOx } from "./ox";
import { createPanels, PanelToggle, Sash, SessionsToggle } from "./Panels";
import type { FileRoot } from "./protocol";
import { SessionView } from "./SessionView";
import { Sidebar } from "./Sidebar";
import type { Chat } from "./TabArea";
import { Workspace } from "./Workspace";

export type Selection = { kind: "session"; id: string } | { kind: "draft"; workspaceId: string };

// sameSelection is whether a and b select the same session or draft.
const sameSelection = (a: Selection | undefined, b: Selection | undefined) =>
  a?.kind === "session" && b?.kind === "session"
    ? a.id === b.id
    : a?.kind === "draft" && b?.kind === "draft"
      ? a.workspaceId === b.workspaceId
      : a === b;

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
  // Selecting the selected session or draft again changes nothing, so the
  // views keyed by the selection stay mounted.
  const [selected, setSelected] = createSignal<Selection | undefined>(selectionFromHash(), { equals: sameSelection });
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
  createEffect(
    () => session()?.id,
    (id) => ox.show(id),
  );
  // A removed workspace's draft is hidden until the effect below drops it.
  const liveDrafts = () => drafts().filter((d) => ox.workspaces().some((w) => w.id === d.workspaceId));
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

  // The tab area and the panel at the right show the selected session's
  // directory, or a draft's workspace, with that session's or draft's tabs and
  // expanded folders.
  const files = (): { root: FileRoot; control: SessionControl } | undefined => {
    const current = session();
    if (current) return { root: { session_id: current.id }, control: ox.control(current.id) };
    const d = draft();
    const workspace = d && ox.workspaces().find((w) => w.id === d.workspaceId);
    return workspace && { root: { workspace_id: workspace.id }, control: d.control };
  };

  // With no session named, the transcript is empty, so a narrow page opens
  // with the session list.
  const panels = createPanels(selectionFromHash() ? undefined : "sessions");
  // Whether the file view wraps long lines, for every file until reload.
  const [wrap, setWrap] = createSignal(false);
  // Panel widths apply only to the wide layout's side-by-side panels.
  const width = (pixels: number) => (panels.narrow() ? undefined : pixels);
  // The chat tab: the selected session's transcript and composer, or the
  // draft's.
  const chat: Chat = {
    label: () => session()?.label ?? (draft() ? "New session" : ""),
    title: () => session()?.directory ?? ox.workspaces().find((w) => w.id === draft()?.workspaceId)?.path,
    view: () => (
      // Keyed, so each session and draft gets its own view and view state.
      <Switch>
        <Match when={session()} keyed>
          {(session) => (
            <Show
              when={session.load.snapshot}
              fallback={
                <p role="status" class="loading">
                  Loading session…
                </p>
              }
            >
              {(snapshot) => <SessionView session={session} snapshot={snapshot()} />}
            </Show>
          )}
        </Match>
        <Match when={draft()} keyed>
          {(current) => <NewSessionView draft={current} onCreated={() => finish(current)} />}
        </Match>
      </Switch>
    ),
  };
  // With nothing selected, one bare strip carries both toggles.
  const BareStrip = () => (
    <section class="group">
      <div class="strip bare">
        <Show when={!panels.sessions()}>
          <SessionsToggle panels={panels} />
        </Show>
        <div class="editor-actions">
          <PanelToggle panels={panels} />
        </div>
      </div>
    </section>
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
    <div class={["layout", { narrow: panels.narrow() }]}>
      <Sidebar
        selected={selected()}
        onSelect={(selection) => {
          select(selection);
          panels.dismiss();
        }}
        width={width(panels.leftWidth())}
        hidden={!panels.sessions()}
        toggle={<SessionsToggle panels={panels} />}
      />
      <Show when={panels.narrow() && (panels.sessions() || panels.right())}>
        {/* biome-ignore lint/a11y/noStaticElementInteractions: a tap outside the drawer closes it; the header's toggles close it from the keyboard. */}
        <div class="scrim" onClick={() => panels.dismiss()} />
      </Show>
      <Show when={panels.sessions()}>
        <Sash label="Resize sessions" drag={(before) => (dx) => panels.setLeftWidth(before + dx)} />
      </Show>
      <div class="center" style={{ "--tree": `${panels.rightWidth()}px` }}>
        <Show when={ox.error()}>
          {(error) => (
            // A dropped stream that is reconnecting is routine, such as after the
            // phone wakes the page, so it shows as a status rather than an error.
            <Switch fallback={<p role="alert">{error().message}</p>}>
              <Match when={error() instanceof Disconnected && !(error() as Disconnected).closed}>
                <p role="status">Reconnecting…</p>
              </Match>
              <Match when={error() instanceof Disconnected}>
                <p role="alert">
                  {error().message}{" "}
                  <button type="button" class="outline" onClick={() => location.reload()}>
                    Reload
                  </button>
                </p>
              </Match>
            </Switch>
          )}
        </Show>
        <For each={liveDrafts().filter((d) => d.control.error() && d !== draft())}>
          {(failed) => (
            <p role="alert">
              {failed.control.error()}{" "}
              <button
                type="button"
                class="outline"
                onClick={() => select({ kind: "draft", workspaceId: failed.workspaceId })}
              >
                Reopen draft
              </button>
            </p>
          )}
        </For>
        <div class="panes">
          {/* Keyed by selection, so the tab area and panel read beneath that
              selection's root. */}
          <Show when={selected()} keyed fallback={<BareStrip />}>
            <Show when={files()} fallback={<BareStrip />}>
              {(current) => (
                <Workspace
                  root={current().root}
                  control={current().control}
                  panels={panels}
                  wrap={wrap()}
                  onWrap={setWrap}
                  chat={chat}
                />
              )}
            </Show>
          </Show>
        </div>
      </div>
    </div>
  );
}
