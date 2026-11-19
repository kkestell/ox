import { createSignal, Match, Show, Switch } from "solid-js";
import { OxProvider, useOx } from "./ox";
import { NewSessionView, SessionView } from "./SessionView";
import { Sidebar } from "./Sidebar";

export type Selection = { kind: "session"; id: string } | { kind: "draft"; workspaceId: string };

export default function App() {
  return (
    <OxProvider>
      <Layout />
    </OxProvider>
  );
}

function Layout() {
  const ox = useOx();
  const [selected, setSelected] = createSignal<Selection>();
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
      <Sidebar selected={selected()} onSelect={setSelected} />
      <main>
        <Show when={ox.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
        {/* Keyed, so each session and draft gets its own view and view state. */}
        <Switch>
          <Match when={session()} keyed>
            {(session) => <SessionView session={session} />}
          </Match>
          <Match when={draft()} keyed>
            {(workspaceId) => (
              <NewSessionView workspaceId={workspaceId} onStarted={(id) => setSelected({ kind: "session", id })} />
            )}
          </Match>
        </Switch>
      </main>
    </>
  );
}
