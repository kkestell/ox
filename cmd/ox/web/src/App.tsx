import { createSignal, Show } from "solid-js";
import { OxProvider, useOx } from "./ox";
import { SessionView } from "./SessionView";
import { Sidebar } from "./Sidebar";

export default function App() {
  return (
    <OxProvider>
      <Layout />
    </OxProvider>
  );
}

function Layout() {
  const ox = useOx();
  const [selected, setSelected] = createSignal<string>();
  const session = () => {
    const id = selected();
    return id === undefined ? undefined : ox.state.sessions[id];
  };

  return (
    <>
      <Sidebar selected={selected()} onSelect={setSelected} />
      <main>
        <Show when={ox.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
        {/* Keyed, so each session gets its own view and view state. */}
        <Show when={session()} keyed>
          {(session) => <SessionView session={session} />}
        </Show>
      </main>
    </>
  );
}
