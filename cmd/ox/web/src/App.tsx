import { createEffect, createSignal, For, Match, Show, Switch } from "solid-js";
import { createDraft, type Draft, NewSessionView } from "./NewSessionView";
import { OxProvider, useOx } from "./ox";
import { SessionView } from "./SessionView";
import { Sidebar } from "./Sidebar";

export type Selection = { kind: "session"; id: string } | { kind: "draft"; workspaceId: string };

// The URL's hash holds the selected session, so a reload keeps it.
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
  const session = () => {
    const s = selected();
    if (s?.kind !== "session") return;
    const session = ox.state.sessions[s.id];
    return session?.id === s.id ? session : undefined;
  };
  const draft = () => {
    const s = selected();
    return s?.kind === "draft" ? drafts().find((d) => d.workspaceId === s.workspaceId) : undefined;
  };

  const finish = (created: Draft) => {
    if (draft() === created) select({ kind: "session", id: created.id });
    setDrafts((all) => all.filter((d) => d !== created));
  };

  // The creation reply can be lost even though the session appears in a
  // notification or reconnect snapshot. Reattach the draft without sending.
  createEffect(
    () =>
      drafts().find(
        (d) => d.control.error() && ox.state.sessions[d.id]?.synced && (d === draft() || session()?.id === d.id),
      ),
    (recovered) => {
      if (recovered) {
        recovered.control.recover();
        finish(recovered);
      }
    },
  );

  return (
    <>
      <Sidebar selected={selected()} onSelect={select} />
      <main>
        <Show when={ox.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
        <For each={drafts().filter((d) => d.control.error() && d !== draft())}>
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
        <Switch>
          <Match when={session()} keyed>
            {(session) => <SessionView session={session} />}
          </Match>
          <Match when={draft()} keyed>
            {(current) => <NewSessionView draft={current} onCreated={() => finish(current)} />}
          </Match>
        </Switch>
      </main>
    </>
  );
}
