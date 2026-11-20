import { createSignal, For, onSettled, Show } from "solid-js";
import type { Selection } from "./App";
import { createAttempt, type Session, sessionLabel, useOx, workspaceName } from "./ox";

type DialogRequest =
  | { kind: "add" }
  | { kind: "delete"; id: string; label: string }
  | { kind: "remove"; id: string; path: string };

// The keyed view gives each opening its own element, target, and attempt.
function WorkspaceDialog(props: { request: DialogRequest; onClose(): void }) {
  const ox = useOx();
  const [error, attempt] = createAttempt();
  const [busy, setBusy] = createSignal(false);
  const request = props.request;
  let dialog!: HTMLDialogElement;
  onSettled(() => dialog.showModal());

  const submit = (event: SubmitEvent & { currentTarget: HTMLFormElement }) => {
    event.preventDefault();
    if (busy()) return;
    const operation =
      request.kind === "add"
        ? ox.addWorkspace(String(new FormData(event.currentTarget).get("path")))
        : request.kind === "delete"
          ? ox.destroy(request.id)
          : ox.removeWorkspace(request.id);
    setBusy(true);
    attempt(operation.then(() => dialog.close())).finally(() => setBusy(false));
  };

  return (
    <dialog ref={dialog} role={request.kind === "add" ? undefined : "alertdialog"} onClose={props.onClose}>
      <form onSubmit={submit}>
        {request.kind === "add" ? (
          <input name="path" placeholder="/absolute/path/to/directory" size="40" required autofocus />
        ) : request.kind === "delete" ? (
          <>
            <h2>Delete session?</h2>
            <p class="description">This deletes "{request.label}" and its history.</p>
          </>
        ) : (
          <>
            <h2>Remove workspace?</h2>
            <p class="description">This removes "{request.path}" from Ox. The directory stays on disk.</p>
          </>
        )}
        <button type="button" onClick={() => dialog.close()}>
          Cancel
        </button>
        <button type="submit" class={{ destructive: request.kind === "delete" }} disabled={busy()}>
          {request.kind === "add" ? "Add" : request.kind === "delete" ? "Delete" : "Remove"}
        </button>
      </form>
      <Show when={error()}>{(message) => <p role="alert">{message()}</p>}</Show>
    </dialog>
  );
}

// SessionRow is a session's row, indented for a child. A session with
// children has no Delete button.
function SessionRow(props: {
  session: Session;
  selected: boolean;
  deletable: boolean;
  onSelect(): void;
  onDelete(): void;
}) {
  return (
    <li class={{ selected: props.selected, child: props.session.child !== undefined }}>
      <button
        type="button"
        class="open"
        aria-current={props.selected ? "page" : undefined}
        onClick={() => props.onSelect()}
      >
        <Show when={props.session.load.snapshot?.state}>
          {(status) => <span class={["status", status()]} role="img" title={status()} aria-label={status()} />}
        </Show>
        <span class="title">{sessionLabel(props.session)}</span>
      </button>
      <Show when={props.deletable}>
        <button
          type="button"
          class="icon trash"
          aria-label="Delete session"
          title="Delete session"
          onClick={() => props.onDelete()}
        />
      </Show>
    </li>
  );
}

export function Sidebar(props: { selected?: Selection; onSelect(selection: Selection): void }) {
  const ox = useOx();
  const [request, setRequest] = createSignal<DialogRequest>();
  const isSelected = (session: Session) => props.selected?.kind === "session" && props.selected.id === session.id;

  return (
    <nav>
      <button
        type="button"
        class="icon folder-plus"
        aria-label="Add workspace"
        title="Add workspace"
        onClick={() => setRequest({ kind: "add" })}
      />
      <Show when={request()} keyed>
        {(current) => <WorkspaceDialog request={current} onClose={() => setRequest(undefined)} />}
      </Show>
      <For each={ox.state.workspaces}>
        {(workspace) => (
          <>
            <p title={workspace.path}>
              <span class="name">{workspaceName(workspace.path)}</span>
              <Show when={workspace.sessions.length === 0}>
                <button
                  type="button"
                  class="icon trash"
                  aria-label="Remove workspace"
                  title="Remove workspace"
                  onClick={() => setRequest({ kind: "remove", id: workspace.id, path: workspace.path })}
                />
              </Show>
              <button
                type="button"
                class="icon plus"
                aria-label="New session"
                title="New session"
                onClick={() => props.onSelect({ kind: "draft", workspaceId: workspace.id })}
              />
            </p>
            <ul>
              <For each={workspace.sessions.filter((session) => !session.child)}>
                {(session) => {
                  const children = () => workspace.sessions.filter((other) => other.child?.parent_id === session.id);
                  return (
                    <>
                      <SessionRow
                        session={session}
                        selected={isSelected(session)}
                        deletable={children().length === 0}
                        onSelect={() => props.onSelect({ kind: "session", id: session.id })}
                        onDelete={() => setRequest({ kind: "delete", id: session.id, label: sessionLabel(session) })}
                      />
                      <For each={children()}>
                        {(child) => (
                          <SessionRow
                            session={child}
                            selected={isSelected(child)}
                            deletable
                            onSelect={() => props.onSelect({ kind: "session", id: child.id })}
                            onDelete={() => setRequest({ kind: "delete", id: child.id, label: sessionLabel(child) })}
                          />
                        )}
                      </For>
                    </>
                  );
                }}
              </For>
            </ul>
          </>
        )}
      </For>
    </nav>
  );
}
