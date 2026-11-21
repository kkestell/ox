import { createMemo, createSignal, For, onSettled, Show } from "solid-js";
import type { Selection } from "./App";
import { createAttempt, type Session, useOx, type Workspace, workspaceName } from "./ox";
import { settled } from "./protocol";

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

// Row is a shown session in the sidebar's tree. depth counts its ancestors;
// rise counts the rows up to where its guide starts, its parent's for its
// first shown child, else its previous shown sibling's.
type Row = { session: Session; depth: number; rise: number; first: boolean; last: boolean };

// rowsOf flattens the shown sessions depth-first, oldest first at every
// level. A shown session's ancestors are shown, since the sessions beneath an
// archived session are archived.
function rowsOf(shown: Session[]): Row[] {
  const rows: Row[] = [];
  const add = (parentId: string | undefined, depth: number, parentRow: number) => {
    const children = shown.filter((session) => session.child?.parent_id === parentId);
    let above = parentRow;
    children.forEach((session, i) => {
      const index = rows.length;
      rows.push({ session, depth, rise: index - above, first: i === 0, last: i === children.length - 1 });
      above = index;
      add(session.id, depth + 1, index);
    });
  };
  add(undefined, 0, -1);
  return rows;
}

// SessionRow is a session's row, indented by its depth. An archived session
// shows an archive icon in place of its status.
function SessionRow(props: { row: Row; selected: boolean; onSelect(): void; onMenu(at: MenuAt): void }) {
  const session = () => props.row.session;
  return (
    <li
      class={{
        selected: props.selected,
        child: props.row.depth > 0,
        first: props.row.first,
        last: props.row.last,
        archived: session().archived,
      }}
      style={{ "--depth": props.row.depth, "--rise": props.row.rise }}
      onContextMenu={(event) => {
        event.preventDefault();
        props.onMenu({ x: event.clientX, y: event.clientY });
      }}
    >
      <button
        type="button"
        class="open"
        aria-current={props.selected ? "page" : undefined}
        onClick={() => props.onSelect()}
      >
        <Show
          when={!session().archived}
          fallback={<span class="icon archive" role="img" title="archived" aria-label="archived" />}
        >
          <Show when={session().load.snapshot?.state}>
            {(status) => <span class={["status", status()]} role="img" title={status()} aria-label={status()} />}
          </Show>
        </Show>
        <span class="title">{session().label}</span>
      </button>
      <MenuButton label="Session actions" onMenu={props.onMenu} />
    </li>
  );
}

type MenuAt = { x: number; y: number };
type MenuRequest = { target: { kind: "session" | "workspace"; id: string } } & MenuAt;
type MenuItem = { label: string; disabledBecause?: string; onChoose(): void };

// MenuButton opens a menu below its bottom-right corner.
function MenuButton(props: { label: string; onMenu(at: MenuAt): void }) {
  return (
    <button
      type="button"
      class="icon ellipsis"
      aria-label={props.label}
      title={props.label}
      onClick={(event) => {
        const box = event.currentTarget.getBoundingClientRect();
        props.onMenu({ x: box.right, y: box.bottom });
      }}
    />
  );
}

// Menu is a popover at the request's coordinates. Light dismiss and Escape
// close it, as does choosing an item. A disabled item gives its reason.
function Menu(props: { at: MenuAt; label: string; items: MenuItem[]; onClose(): void }) {
  let menu!: HTMLDivElement;
  onSettled(() => {
    menu.showPopover();
    // Keep the whole menu on screen, as near the request's point as it fits,
    // as its size changes.
    const place = () => {
      const { width, height } = menu.getBoundingClientRect();
      menu.style.left = `${Math.max(0, Math.min(props.at.x, innerWidth - width))}px`;
      menu.style.top = `${Math.max(0, Math.min(props.at.y, innerHeight - height))}px`;
    };
    place();
    const observer = new ResizeObserver(place);
    observer.observe(menu);
    menu.querySelector<HTMLButtonElement>("[role=menuitem]:not(:disabled)")?.focus();
    return () => observer.disconnect();
  });
  return (
    <div
      ref={menu}
      popover="auto"
      role="menu"
      aria-label={props.label}
      class="menu"
      onToggle={(event) => {
        if (event.newState !== "open") props.onClose();
      }}
    >
      <For each={props.items}>
        {(item) => (
          <button
            type="button"
            role="menuitem"
            disabled={item.disabledBecause !== undefined}
            onClick={() => {
              menu.hidePopover();
              item.onChoose();
            }}
          >
            <span>{item.label}</span>
            <Show when={item.disabledBecause}>{(reason) => <span class="reason">{reason()}</span>}</Show>
          </button>
        )}
      </For>
    </div>
  );
}

export function Sidebar(props: { selected?: Selection; onSelect(selection: Selection): void }) {
  const ox = useOx();
  const [request, setRequest] = createSignal<DialogRequest>();
  const [menu, setMenu] = createSignal<MenuRequest>();
  // shown holds the workspaces whose archived sessions are shown.
  const [shown, setShown] = createSignal(new Set<string>());
  const [error, attempt] = createAttempt();
  const isSelected = (session: Session) => props.selected?.kind === "session" && props.selected.id === session.id;
  const childrenOf = (session: Session) =>
    ox.workspaceOf(session.id)?.sessions.filter((other) => other.child?.parent_id === session.id) ?? [];
  const descendantsOf = (session: Session): Session[] =>
    childrenOf(session).flatMap((child) => [child, ...descendantsOf(child)]);
  const visible = (workspaceId: string, session: Session) => !session.archived || shown().has(workspaceId);

  // oxd archives a family only when each member is settled.
  const archiveBlocked = (session: Session) => {
    if (!ox.ready()) return "Not connected";
    const descendants = descendantsOf(session);
    const free = [session, ...descendants].every((s) => s.load.kind === "live" && settled(s.load.snapshot.state));
    if (free) return undefined;
    return descendants.length ? "Wait until it and its child sessions are idle" : "Wait until it is idle";
  };
  const sessionItems = (session: Session): MenuItem[] => {
    const parent = session.child && ox.session(session.child.parent_id);
    const archiveItem: MenuItem = session.archived
      ? {
          label: "Unarchive",
          disabledBecause: parent?.archived ? "Unarchive its parent first" : ox.ready() ? undefined : "Not connected",
          onChoose: () => attempt(ox.unarchive(session.id)),
        }
      : { label: "Archive", disabledBecause: archiveBlocked(session), onChoose: () => attempt(ox.archive(session.id)) };
    return [
      archiveItem,
      {
        label: "Delete",
        disabledBecause: childrenOf(session).length ? "Delete its child sessions first" : undefined,
        onChoose: () => setRequest({ kind: "delete", id: session.id, label: session.label }),
      },
    ];
  };
  const workspaceItems = (workspace: Workspace): MenuItem[] => {
    const showing = shown().has(workspace.id);
    return [
      {
        label: showing ? "Hide archived" : "Show archived",
        onChoose: () =>
          setShown((ids) => {
            const next = new Set(ids);
            if (showing) next.delete(workspace.id);
            else next.add(workspace.id);
            return next;
          }),
      },
      {
        label: "Remove workspace",
        disabledBecause: !workspace.sessions.length
          ? undefined
          : workspace.sessions.some((s) => s.archived)
            ? "Delete its sessions first, including archived ones"
            : "Delete its sessions first",
        onChoose: () => setRequest({ kind: "remove", id: workspace.id, path: workspace.path }),
      },
    ];
  };
  // The open menu's label and items, read from the store so they follow it
  // while open; none once its target is gone.
  const openMenu = () => {
    const current = menu();
    if (!current) return;
    const { kind, id } = current.target;
    if (kind === "session") {
      const session = ox.session(id);
      return session && { request: current, label: "Session actions", items: sessionItems(session) };
    }
    const workspace = ox.state.workspaces.find((w) => w.id === id);
    return workspace && { request: current, label: "Workspace actions", items: workspaceItems(workspace) };
  };
  const menuOf = (kind: "session" | "workspace", id: string) => (at: MenuAt) =>
    setMenu({ target: { kind, id }, ...at });

  return (
    <nav>
      <button
        type="button"
        class="icon folder-plus"
        aria-label="Add workspace"
        title="Add workspace"
        onClick={() => setRequest({ kind: "add" })}
      />
      <Show when={error()}>{(message) => <p role="alert">{message()}</p>}</Show>
      <Show when={request()} keyed>
        {(current) => <WorkspaceDialog request={current} onClose={() => setRequest(undefined)} />}
      </Show>
      {/* Keyed by request, so each opening gets its own popover; a stale
          close leaves a newer request open. */}
      <Show when={openMenu()?.request} keyed>
        {(current) => (
          <Show when={openMenu()}>
            {(open) => (
              <Menu
                at={current}
                label={open().label}
                items={open().items}
                onClose={() => setMenu((m) => (m === current ? undefined : m))}
              />
            )}
          </Show>
        )}
      </Show>
      <For each={ox.state.workspaces}>
        {(workspace) => {
          const rows = createMemo(() => rowsOf(workspace.sessions.filter((session) => visible(workspace.id, session))));
          return (
            <>
              <p
                title={workspace.path}
                onContextMenu={(event) => {
                  event.preventDefault();
                  menuOf("workspace", workspace.id)({ x: event.clientX, y: event.clientY });
                }}
              >
                <span class="name">{workspaceName(workspace.path)}</span>
                <MenuButton label="Workspace actions" onMenu={menuOf("workspace", workspace.id)} />
                <button
                  type="button"
                  class="icon plus"
                  aria-label="New session"
                  title="New session"
                  onClick={() => props.onSelect({ kind: "draft", workspaceId: workspace.id })}
                />
              </p>
              <ul>
                <For each={rows()} keyed={(row) => row.session.id}>
                  {(row) => (
                    <SessionRow
                      row={row()}
                      selected={isSelected(row().session)}
                      onSelect={() => props.onSelect({ kind: "session", id: row().session.id })}
                      onMenu={menuOf("session", row().session.id)}
                    />
                  )}
                </For>
              </ul>
            </>
          );
        }}
      </For>
    </nav>
  );
}
