import type { JSX } from "@solidjs/web";
import { createMemo, createSignal, createUniqueId, For, onSettled, Show } from "solid-js";
import type { Selection } from "./App";
import { createAttempt, directoryName, type Session, useOx, type Workspace } from "./ox";
import { settled } from "./protocol";

type DialogRequest =
  | { kind: "add" }
  | { kind: "delete"; id: string; label: string; worktree: boolean }
  | { kind: "remove"; id: string; path: string };

// The keyed view gives each opening its own element, target, and attempt.
// Adding a workspace waits in the dialog for oxd, which checks the directory;
// a deletion or removal closes the dialog at once and reports its request
// through onRequest.
function WorkspaceDialog(props: {
  request: DialogRequest;
  onClose(): void;
  onRequest(request: Promise<unknown>): void;
}) {
  const ox = useOx();
  const [error, attempt] = createAttempt();
  const [busy, setBusy] = createSignal(false);
  const request = props.request;
  const id = createUniqueId();
  let dialog!: HTMLDialogElement;
  onSettled(() => dialog.showModal());

  const submit = (event: SubmitEvent & { currentTarget: HTMLFormElement }) => {
    event.preventDefault();
    if (request.kind !== "add") {
      dialog.close();
      props.onRequest(request.kind === "delete" ? ox.destroy(request.id) : ox.removeWorkspace(request.id));
      return;
    }
    if (busy()) return;
    setBusy(true);
    const operation = ox.addWorkspace(String(new FormData(event.currentTarget).get("path")));
    attempt(operation.then(() => dialog.close())).finally(() => setBusy(false));
  };

  return (
    <dialog
      ref={dialog}
      role={request.kind === "add" ? undefined : "alertdialog"}
      aria-label={request.kind === "add" ? "Add workspace" : undefined}
      aria-labelledby={request.kind === "add" ? undefined : `${id}-title`}
      aria-describedby={request.kind === "add" ? undefined : `${id}-description`}
      onClose={props.onClose}
    >
      <form onSubmit={submit}>
        {request.kind === "add" ? (
          <input
            name="path"
            aria-label="Directory"
            placeholder="/absolute/path/to/directory"
            size="40"
            required
            autofocus
          />
        ) : request.kind === "delete" ? (
          <>
            <h2 id={`${id}-title`}>Delete session?</h2>
            <p id={`${id}-description`} class="description">
              {request.worktree
                ? `This deletes "${request.label}", its history, and its worktree. The branch and its commits are not deleted.`
                : `This deletes "${request.label}" and its history.`}
            </p>
          </>
        ) : (
          <>
            <h2 id={`${id}-title`}>Remove workspace?</h2>
            <p id={`${id}-description`} class="description">
              This removes "{request.path}" from Ox. The directory stays on disk.
            </p>
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
// first shown child, else its previous shown sibling's. children says whether
// it has shown children and whether they show.
type Row = {
  session: Session;
  depth: number;
  rise: number;
  first: boolean;
  last: boolean;
  children: "none" | "shown" | "hidden";
};

// rowsOf flattens the shown sessions depth-first in oxd's creation order:
// top-level sessions newest first, and each session's children oldest first
// beneath it. It does not descend into a collapsed session. A shown session's
// ancestors are shown, since the sessions beneath an archived session are
// archived.
function rowsOf(shown: Session[], collapsed: Set<string>): Row[] {
  const rows: Row[] = [];
  const childrenOf = (parentId: string | undefined) => shown.filter((session) => session.child?.parent_id === parentId);
  const add = (children: Session[], depth: number, parentRow: number) => {
    let above = parentRow;
    children.forEach((session, i) => {
      const index = rows.length;
      const own = childrenOf(session.id);
      const state = !own.length ? "none" : collapsed.has(session.id) ? "hidden" : "shown";
      rows.push({
        session,
        depth,
        rise: index - above,
        first: i === 0,
        last: i === children.length - 1,
        children: state,
      });
      above = index;
      if (state === "shown") add(own, depth + 1, index);
    });
  };
  add(childrenOf(undefined).reverse(), 0, -1);
  return rows;
}

// SessionRow is a session's row, indented by its depth. An archived session
// shows an archive icon in place of its status. The row's button is named by
// its title and described by its status. The title runs to the row's edge,
// beneath the actions' fade. A session with shown children has a chevron after
// its title that hides and shows them. A click anywhere on the row but its
// other buttons opens the session, as its button does.
function SessionRow(props: {
  row: Row;
  selected: boolean;
  onSelect(): void;
  onToggle(): void;
  onMenu(at: MenuAt): void;
}) {
  const ox = useOx();
  const session = () => props.row.session;
  const archived = () => ox.archived(session());
  const id = createUniqueId();
  return (
    // biome-ignore lint/a11y/useAriaPropsSupportedByRole: WAI-ARIA 1.2 gives listitem aria-level, the session's depth in the tree.
    <li
      aria-level={props.row.depth + 1}
      class={{
        selected: props.selected,
        child: props.row.depth > 0,
        first: props.row.first,
        last: props.row.last,
        archived: archived(),
      }}
      style={{ "--depth": props.row.depth, "--rise": props.row.rise }}
      onClick={(event) => {
        if (event.target instanceof Element && !event.target.closest("button")) props.onSelect();
      }}
      onContextMenu={(event) => {
        event.preventDefault();
        props.onMenu({ x: event.clientX, y: event.clientY });
      }}
    >
      <div class="head">
        <button
          type="button"
          class="open"
          aria-current={props.selected ? "page" : undefined}
          aria-labelledby={`${id}-title`}
          aria-describedby={`${id}-status`}
          onClick={() => props.onSelect()}
        >
          <Show
            when={!archived()}
            fallback={
              <span id={`${id}-status`} class="icon archive" role="img" title="archived" aria-label="archived" />
            }
          >
            <span
              id={`${id}-status`}
              class={["status", session().state]}
              role="img"
              title={session().state}
              aria-label={session().state}
            />
          </Show>
          <span id={`${id}-title`} class="title">
            {session().label}
          </span>
        </button>
        <Show when={props.row.children !== "none"}>
          <button
            type="button"
            class="icon chevron-right"
            aria-label="Child sessions"
            title="Child sessions"
            aria-expanded={props.row.children === "shown" ? "true" : "false"}
            onClick={() => props.onToggle()}
          />
        </Show>
      </div>
      <div class="actions">
        <MenuButton label="Session actions" onMenu={props.onMenu} />
      </div>
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
          <MenuItemButton
            item={item}
            onChoose={() => {
              menu.hidePopover();
              item.onChoose();
            }}
          />
        )}
      </For>
    </div>
  );
}

// MenuItemButton is named by its label and described by its reason.
function MenuItemButton(props: { item: MenuItem; onChoose(): void }) {
  const id = createUniqueId();
  return (
    <button
      type="button"
      role="menuitem"
      disabled={props.item.disabledBecause !== undefined}
      aria-labelledby={`${id}-label`}
      aria-describedby={`${id}-reason`}
      onClick={() => props.onChoose()}
    >
      <span id={`${id}-label`}>{props.item.label}</span>
      <Show when={props.item.disabledBecause}>
        {(reason) => (
          <span id={`${id}-reason`} class="reason">
            {reason()}
          </span>
        )}
      </Show>
    </button>
  );
}

export function Sidebar(props: {
  selected?: Selection;
  onSelect(selection: Selection): void;
  width?: number;
  hidden: boolean;
  toggle: JSX.Element;
}) {
  const ox = useOx();
  const [request, setRequest] = createSignal<DialogRequest>();
  const [menu, setMenu] = createSignal<MenuRequest>();
  // shown holds the workspaces whose archived sessions are shown.
  const [shown, setShown] = createSignal(new Set<string>());
  // collapsed holds the sessions whose children are hidden.
  const [collapsed, setCollapsed] = createSignal(new Set<string>());
  const toggle = (id: string) =>
    setCollapsed((ids) => {
      const next = new Set(ids);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  const [error, attempt] = createAttempt();
  const isSelected = (session: Session) => props.selected?.kind === "session" && props.selected.id === session.id;
  const childrenOf = (session: Session) =>
    ox.workspaceOf(session.id)?.sessions.filter((other) => other.child?.parent_id === session.id) ?? [];
  const descendantsOf = (session: Session): Session[] =>
    childrenOf(session).flatMap((child) => [child, ...descendantsOf(child)]);
  const visible = (workspaceId: string, session: Session) => !ox.archived(session) || shown().has(workspaceId);

  // oxd archives a family only when each member is settled.
  const archiveBlocked = (session: Session) => {
    if (!ox.ready()) return "Not connected";
    const descendants = descendantsOf(session);
    if ([session, ...descendants].every((s) => settled(s.state))) return undefined;
    return descendants.length ? "Wait until it and its child sessions are idle" : "Wait until it is idle";
  };
  // A session's items wait while it or an ancestor has a request in flight.
  const sessionItems = (session: Session): MenuItem[] => {
    const parent = session.child && ox.session(session.child.parent_id);
    const waiting = ox.busy(session.id) ? "Wait until Ox answers" : undefined;
    const archiveItem: MenuItem = ox.archived(session)
      ? {
          label: "Unarchive",
          disabledBecause:
            waiting ?? (parent?.archived ? "Unarchive its parent first" : ox.ready() ? undefined : "Not connected"),
          onChoose: () => attempt(ox.unarchive(session.id)),
        }
      : {
          label: "Archive",
          disabledBecause: waiting ?? archiveBlocked(session),
          onChoose: () => attempt(ox.archive(session.id)),
        };
    return [
      archiveItem,
      {
        label: "Delete",
        disabledBecause: waiting ?? (childrenOf(session).length ? "Delete its child sessions first" : undefined),
        onChoose: () =>
          setRequest({ kind: "delete", id: session.id, label: session.label, worktree: !!session.worktree }),
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
    const workspace = ox.workspaces().find((w) => w.id === id);
    return workspace && { request: current, label: "Workspace actions", items: workspaceItems(workspace) };
  };
  const menuOf = (kind: "session" | "workspace", id: string) => (at: MenuAt) =>
    setMenu({ target: { kind, id }, ...at });

  return (
    <nav
      aria-label="Sessions"
      hidden={props.hidden}
      style={{ width: props.width === undefined ? undefined : `${props.width}px` }}
    >
      <div class="bar">{props.toggle}</div>
      <Show when={request()} keyed>
        {(current) => <WorkspaceDialog request={current} onClose={() => setRequest(undefined)} onRequest={attempt} />}
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
      <div class="workspaces">
        <Show when={error()}>{(message) => <p role="alert">{message()}</p>}</Show>
        <For each={ox.workspaces()}>
          {(workspace) => {
            const rows = createMemo(() =>
              rowsOf(
                ox.sessionsOf(workspace).filter((session) => visible(workspace.id, session)),
                collapsed(),
              ),
            );
            const id = createUniqueId();
            return (
              <section aria-labelledby={id}>
                {/* biome-ignore lint/a11y/noStaticElementInteractions: a right-click opens the menu that Workspace actions opens from the keyboard. */}
                <header
                  title={workspace.path}
                  onContextMenu={(event) => {
                    event.preventDefault();
                    menuOf("workspace", workspace.id)({ x: event.clientX, y: event.clientY });
                  }}
                >
                  <h2 id={id} class="name">
                    {directoryName(workspace.path)}
                  </h2>
                  <div class="actions">
                    <MenuButton label="Workspace actions" onMenu={menuOf("workspace", workspace.id)} />
                    <button
                      type="button"
                      class="icon plus"
                      aria-label="New session"
                      title="New session"
                      onClick={() => props.onSelect({ kind: "draft", workspaceId: workspace.id })}
                    />
                  </div>
                </header>
                <ul>
                  <For each={rows()} keyed={(row) => row.session.id}>
                    {(row) => (
                      <SessionRow
                        row={row()}
                        selected={isSelected(row().session)}
                        onSelect={() => props.onSelect({ kind: "session", id: row().session.id })}
                        onToggle={() => toggle(row().session.id)}
                        onMenu={menuOf("session", row().session.id)}
                      />
                    )}
                  </For>
                </ul>
              </section>
            );
          }}
        </For>
      </div>
      <footer class="bar">
        <button
          type="button"
          class="icon folder-plus"
          aria-label="Add workspace"
          title="Add workspace"
          onClick={() => setRequest({ kind: "add" })}
        />
      </footer>
    </nav>
  );
}
