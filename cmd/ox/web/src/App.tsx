import { createSignal, Match, Show, Switch } from "solid-js";
import { OxProvider, useOx } from "./ox";
import { NewSessionView, SessionView } from "./SessionView";
import { Sidebar } from "./Sidebar";

export type Selection = { kind: "session"; id: string } | { kind: "draft"; workspaceId: string };

// The URL's hash holds the selected session, so a reload keeps it.
function selectionFromHash(): Selection | undefined {
  const id = decodeURIComponent(location.hash.slice(1));
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
  const select = (selection: Selection) => {
    setSelected(selection);
    const hash = selection.kind === "session" ? `#${encodeURIComponent(selection.id)}` : "";
    history.replaceState(null, "", location.pathname + location.search + hash);
  };
  const session = () => {
    const s = selected();
    return s?.kind === "session" ? ox.state.sessions[s.id] : undefined;
  };
  const draft = () => {
    const s = selected();
    return s?.kind === "draft" ? s.workspaceId : undefined;
  };

  return (
    <>
      <Sidebar selected={selected()} onSelect={select} />
      <main>
        <Show when={ox.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
        {/* Keyed, so each session and draft gets its own view and view state. */}
        <Switch>
          <Match when={session()} keyed>
            {(session) => <SessionView session={session} />}
          </Match>
          <Match when={draft()} keyed>
            {(workspaceId) => (
              <NewSessionView workspaceId={workspaceId} onSent={(id) => select({ kind: "session", id })} />
            )}
          </Match>
        </Switch>
      </main>
    </>
  );
}
