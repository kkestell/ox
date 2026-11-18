import { createSignal, For, Show } from "solid-js";
import { useOx } from "./ox";
import type { Selection } from "./App";

export function Sidebar(props: { selected?: Selection; onSelect(selection: Selection): void }) {
  const ox = useOx();
  const [error, setError] = createSignal<string>();
  let dialog!: HTMLDialogElement;

  const open = () => {
    setError(undefined);
    dialog.showModal();
  };

  const add = async (event: SubmitEvent & { currentTarget: HTMLFormElement }) => {
    event.preventDefault();
    const form = event.currentTarget;
    setError(undefined);
    try {
      await ox.addWorkspace(String(new FormData(form).get("path")));
      form.reset();
      dialog.close();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <nav>
      <button onClick={open}>Add workspace</button>
      <dialog ref={dialog}>
        <form onSubmit={add}>
          <input name="path" placeholder="/absolute/path/to/directory" size="40" required autofocus />
          <button>Add</button>
          <button type="button" onClick={() => dialog.close()}>
            Cancel
          </button>
        </form>
        <Show when={error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      </dialog>
      <For each={ox.state.workspaces}>
        {(workspace) => (
          <>
            <p>
              {workspace.path}{" "}
              <button onClick={() => props.onSelect({ kind: "draft", workspaceId: workspace.id })}>New session</button>
            </p>
            <ul>
              <For each={Object.values(ox.state.sessions).filter((s) => s.workspaceId === workspace.id)}>
                {(session) => (
                  <li
                    class={{ selected: props.selected?.kind === "session" && props.selected.id === session.id }}
                    onClick={() => props.onSelect({ kind: "session", id: session.id })}
                  >
                    <span class={["status", session.state]} role="img" title={session.state} aria-label={session.state} />{" "}
                    {session.id.slice(0, 8)}
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
