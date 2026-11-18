import { createSignal, For, Show } from "solid-js";
import { useOx } from "./ox";
import { SettingsFields, settingsFrom } from "./SettingsFields";

// ox serves the page with the directory it runs in.
const workspace = document.querySelector<HTMLMetaElement>('meta[name="workspace"]')!.content;

export function Sidebar(props: { selected?: string; onSelect(id: string): void }) {
  const ox = useOx();
  const [error, setError] = createSignal<string>();

  const create = async (event: SubmitEvent & { currentTarget: HTMLFormElement }) => {
    event.preventDefault();
    setError(undefined);
    const form = new FormData(event.currentTarget);
    try {
      props.onSelect(await ox.createSession(String(form.get("workspace")), settingsFrom(form)));
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <nav>
      <form onSubmit={create}>
        <input name="workspace" defaultValue={workspace} placeholder="Workspace" size="30" required />
        <SettingsFields value={{ model: "haiku", effort: "medium", permission_mode: "manual" }} />
        <button>New session</button>
      </form>
      <Show when={error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <For each={ox.state.workspaces}>
        {(workspace) => (
          <>
            <p>{workspace.path}</p>
            <ul>
              <For each={Object.values(ox.state.sessions).filter((s) => s.workspaceId === workspace.id)}>
                {(session) => (
                  <li class={{ selected: session.id === props.selected }} onClick={() => props.onSelect(session.id)}>
                    {session.id.slice(0, 8)} {session.state}
                    {session.approvals.length ? " (approval)" : ""}
                  </li>
                )}
              </For>
            </ul>
          </>
        )}
      </For>
    </nav>
  );
}
