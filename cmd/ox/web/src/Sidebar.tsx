import { createSignal, For, Show } from "solid-js";
import type { Selection } from "./App";
import { createAttempt, type Session, useOx } from "./ox";

export function Sidebar(props: { selected?: Selection; onSelect(selection: Selection): void }) {
  const ox = useOx();
  const [addError, attemptAdd, clearAddError] = createAttempt();
  const [deleteError, attemptDelete, clearDeleteError] = createAttempt();
  const [deleting, setDeleting] = createSignal<Session>();
  let addDialog!: HTMLDialogElement;
  let deleteDialog!: HTMLDialogElement;

  const add = (event: SubmitEvent & { currentTarget: HTMLFormElement }) => {
    event.preventDefault();
    const form = event.currentTarget;
    attemptAdd(
      ox.addWorkspace(String(new FormData(form).get("path"))).then(() => {
        form.reset();
        addDialog.close();
      }),
    );
  };

  const sessionsOf = (workspaceId: string) =>
    Object.values(ox.state.sessions)
      .filter((s) => s.workspaceId === workspaceId)
      .sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));

  const isSelected = (session: Session) => props.selected?.kind === "session" && props.selected.id === session.id;

  const confirmDelete = (session: Session) => {
    clearDeleteError();
    setDeleting(session);
    deleteDialog.showModal();
  };

  const destroy = (event: SubmitEvent) => {
    event.preventDefault();
    const session = deleting();
    if (session) attemptDelete(ox.destroy(session.id).then(() => deleteDialog.close()));
  };

  return (
    <nav>
      <button
        type="button"
        class="icon folder-plus"
        aria-label="Add workspace"
        title="Add workspace"
        onClick={() => {
          clearAddError();
          addDialog.showModal();
        }}
      />
      <dialog ref={addDialog}>
        <form onSubmit={add}>
          <input name="path" placeholder="/absolute/path/to/directory" size="40" required autofocus />
          <button type="button" onClick={() => addDialog.close()}>
            Cancel
          </button>
          <button type="submit">Add</button>
        </form>
        <Show when={addError()}>{(error) => <p role="alert">{error()}</p>}</Show>
      </dialog>
      <dialog ref={deleteDialog} role="alertdialog">
        <form onSubmit={destroy}>
          <h2>Delete session?</h2>
          <Show when={deleting()}>
            {(session) => <p class="description">This deletes "{title(session())}" and its history.</p>}
          </Show>
          <button type="button" onClick={() => deleteDialog.close()}>
            Cancel
          </button>
          <button type="submit" class="destructive">
            Delete
          </button>
        </form>
        <Show when={deleteError()}>{(error) => <p role="alert">{error()}</p>}</Show>
      </dialog>
      <For each={ox.state.workspaces}>
        {(workspace) => (
          <>
            <p title={workspace.path}>
              <span class="name">{workspace.path.split("/").at(-1) || workspace.path}</span>
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
                  <li class={{ selected: isSelected(session) }}>
                    <button
                      type="button"
                      class="open"
                      aria-current={isSelected(session) ? "page" : undefined}
                      onClick={() => props.onSelect({ kind: "session", id: session.id })}
                    >
                      <span
                        class={["status", session.state]}
                        role="img"
                        title={session.state}
                        aria-label={session.state}
                      />
                      <span class="title">{title(session)}</span>
                    </button>
                    <button
                      type="button"
                      class="icon trash"
                      aria-label="Delete session"
                      title="Delete session"
                      onClick={() => confirmDelete(session)}
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

function title(session: Session) {
  const prompt = session.entries.find((e) => e.kind === "prompt");
  return prompt?.kind === "prompt" ? prompt.text.trim().split("\n")[0] : session.id.slice(0, 8);
}
