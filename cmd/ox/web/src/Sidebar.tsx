import { createSignal, For, Show } from "solid-js";
import type { Selection } from "./App";
import { type Session, useOx } from "./ox";

export function Sidebar(props: { selected?: Selection; onSelect(selection: Selection): void }) {
  const ox = useOx();
  const [error, setError] = createSignal<string>();
  const [deleteError, setDeleteError] = createSignal<string>();
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

  // A workspace's sessions, oldest first.
  const sessionsOf = (workspaceId: string) =>
    Object.values(ox.state.sessions)
      .filter((s) => s.workspaceId === workspaceId)
      .sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));

  // The destroyed event removes the session from every page.
  const destroy = async (event: MouseEvent, id: string) => {
    event.stopPropagation();
    if (!confirm(`Delete session ${id} and its history?`)) return;
    setDeleteError(undefined);
    try {
      await ox.destroy(id);
    } catch (e) {
      setDeleteError((e as Error).message);
    }
  };

  return (
    <nav>
      <button type="button" class="icon folder-plus" aria-label="Add workspace" title="Add workspace" onClick={open} />
      <dialog ref={dialog}>
        <form onSubmit={add}>
          <input name="path" placeholder="/absolute/path/to/directory" size="40" required autofocus />
          <button type="button" onClick={() => dialog.close()}>
            Cancel
          </button>
          <button type="submit">Add</button>
        </form>
        <Show when={error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      </dialog>
      <Show when={deleteError()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <For each={ox.state.workspaces}>
        {(workspace) => (
          <>
            <p title={workspace.path}>
              <span class="name">{workspace.path.split("/").at(-1)}</span>
              <button
                type="button"
                class="icon plus"
                aria-label="New session"
                title="New session"
                onClick={() => props.onSelect({ kind: "draft", workspaceId: workspace.id })}
              />
            </p>
            <ul>
              <For each={sessionsOf(workspace.id)}>
                {(session) => (
                  <li
                    class={{ selected: props.selected?.kind === "session" && props.selected.id === session.id }}
                    onClick={() => props.onSelect({ kind: "session", id: session.id })}
                  >
                    <span
                      class={["status", session.state]}
                      role="img"
                      title={session.state}
                      aria-label={session.state}
                    />
                    <span class="title">{title(session)}</span>
                    <button
                      type="button"
                      class="icon trash"
                      aria-label="Delete session"
                      title="Delete session"
                      onClick={(event) => destroy(event, session.id)}
                    />
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

// title is the first line of a session's first prompt, or the start of its ID
// before it has one.
function title(session: Session) {
  const prompt = session.entries.find((e) => e.kind === "prompt");
  return prompt?.kind === "prompt" ? prompt.text.trim().split("\n")[0] : session.id.slice(0, 8);
}
