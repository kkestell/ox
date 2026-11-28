import { createContext, createSignal, createStore, onSettled, type ParentProps, reconcile, useContext } from "solid-js";
import { type Connection, connect, Disconnected, type Rpc, RpcError } from "./connection";
import {
  type Child,
  type Defaults,
  type FileRoot,
  type Harness,
  type Methods,
  type Notification,
  notFound,
  type Option,
  type SessionState,
} from "./protocol";
import { createSessionControl, type SessionControl } from "./sessionControl";
import { apply, type SessionSnapshot, settingsOf, snapshotOf } from "./snapshot";

// StoredWorkspace and StoredSession are oxd's session list as the page holds
// it. Views read the list through createOx's projections, which show the
// requests the page has in flight, so they receive Workspace and Session,
// which leave out the raw membership and the archived and pinned flags.
type StoredWorkspace = { id: string; path: string; sessions: StoredSession[] };

type StoredSession = {
  id: string;
  harness: string;
  // directory is where the session's harness runs.
  directory: string;
  // worktree is the root of the git worktree the session was created in.
  worktree?: string;
  child?: Child;
  // label is the session's label, as oxd lists and announces it.
  label: string;
  archived: boolean;
  // pinned is set only for a top-level session.
  pinned: boolean;
  // state is the state oxd last published for the session, which the page
  // follows whether or not the session is loaded. Only the session the page
  // shows has a snapshot.
  state: SessionState;
  load:
    | { kind: "unloaded"; snapshot?: undefined }
    // failure is the last failed request for the snapshot, which the page
    // repeats until it is answered.
    | { kind: "loading"; snapshot?: SessionSnapshot; failure?: string }
    | { kind: "live"; snapshot: SessionSnapshot };
};

export type Workspace = Omit<StoredWorkspace, "sessions">;

export type Session = Omit<StoredSession, "archived" | "pinned">;

// directoryName is the name of a directory.
export const directoryName = (path: string) => path.replace(/\/+$/, "").split("/").at(-1) || path;

// ListRequest is a session list request the page has in flight for a session.
type ListRequest = "archive" | "unarchive" | "destroy" | "pin" | "unpin";

function createOx() {
  const [state, setStore] = createStore<{
    harnesses: Record<string, Harness>;
    permission_modes: Option[];
    defaults?: Defaults;
    workspaces: StoredWorkspace[];
  }>({
    harnesses: {},
    permission_modes: [],
    workspaces: [],
  });
  // listing holds the session list's requests in flight, at most one for each
  // session or workspace, until each settles.
  const [listing, setListing] = createStore<{
    sessions: Record<string, ListRequest>;
    workspaces: Record<string, "remove">;
  }>({ sessions: {}, workspaces: {} });
  const [error, setError] = createSignal<Error>();
  const controls = new Map<string, SessionControl>();
  // live is the stream whose reload has installed the catalog and workspace
  // list, and holds that stream's rpc.
  const [live, setLive] = createSignal<{ rpc: Rpc }>();
  const ready = () => live() !== undefined;
  let connection!: Connection;

  const workspaceOf = (id: string): Workspace | undefined => containing(state.workspaces, id);
  const containing = (workspaces: StoredWorkspace[], id: string) =>
    workspaces.find((w) => w.sessions.some((s) => s.id === id));
  const sessionIn = (workspaces: StoredWorkspace[], id: string) =>
    containing(workspaces, id)?.sessions.find((s) => s.id === id);
  const stored = (id: string) => sessionIn(state.workspaces, id);
  const storedWorkspace = (id: string) => state.workspaces.find((w) => w.id === id);
  // lineOf is a session and its ancestors, nearest first.
  function* lineOf(session: StoredSession | undefined) {
    for (let s = session; s; s = s.child && stored(s.child.parent_id)) yield s;
  }

  // The page shows a session list request's result while it is in flight: a
  // session being deleted and a workspace being removed are gone, and a
  // session being archived or unarchived, with every session beneath it,
  // shows as it will be, as does a session being pinned or unpinned.
  const session = (id: string): Session | undefined => (listing.sessions[id] === "destroy" ? undefined : stored(id));
  const workspaces = (): Workspace[] => state.workspaces.filter((w) => listing.workspaces[w.id] === undefined);
  const sessionsOf = (workspace: Workspace): Session[] =>
    storedWorkspace(workspace.id)?.sessions.filter((s) => listing.sessions[s.id] !== "destroy") ?? [];
  const archived = (session: Session) => {
    for (const s of lineOf(stored(session.id))) {
      const request = listing.sessions[s.id];
      if (request === "archive") return true;
      if (request === "unarchive") return false;
    }
    return stored(session.id)?.archived ?? false;
  };
  const pinned = (session: Session) => {
    const request = listing.sessions[session.id];
    if (request === "pin") return true;
    if (request === "unpin") return false;
    return stored(session.id)?.pinned ?? false;
  };
  // busy is whether a session or an ancestor has a session list request in
  // flight.
  const busy = (id: string) => [...lineOf(stored(id))].some((s) => listing.sessions[s.id] !== undefined);
  // workspaceBusy is whether any session in a workspace, including one being
  // deleted, has a session list request in flight.
  const workspaceBusy = (id: string) => storedWorkspace(id)?.sessions.some((s) => busy(s.id)) ?? false;
  // listsWorkspace is whether oxd lists a workspace, whatever the page has in
  // flight.
  const listsWorkspace = (id: string) => storedWorkspace(id) !== undefined;
  // catalog is what oxd offers, the part of the store views read directly.
  const catalog: {
    readonly harnesses: Record<string, Harness>;
    readonly permission_modes: Option[];
    readonly defaults?: Defaults;
  } = state;

  // requestFor sends a session list request for a session, and returns its
  // promise once it settles.
  function requestFor(id: string, kind: ListRequest) {
    setListing((l) => {
      l.sessions[id] = kind;
    });
    return connection.rpc(`session/${kind}`, { session_id: id }).finally(() =>
      setListing((l) => {
        delete l.sessions[id];
      }),
    );
  }
  const unarchive = (id: string) => requestFor(id, "unarchive");

  // control returns the page's control of a session, which may not be loaded
  // yet.
  function control(id: string) {
    let c = controls.get(id);
    if (!c) {
      c = createSessionControl(
        id,
        (method, params) => connection.rpc(method, params),
        () => {
          const load = stored(id)?.load;
          return load?.kind === "live" ? load.snapshot : undefined;
        },
        () => stored(id)?.state,
        ready,
        () => busy(id),
        () => unarchive(id),
      );
      controls.set(id, c);
    }
    return c;
  }

  // release forgets the page's control of a session that will never exist.
  const release = (id: string) => controls.delete(id);

  // setState writes the state oxd published for a session, from the session
  // list, a session/created, or a state or parked update.
  function setState(id: string, sessionState: SessionState) {
    setStore((s) => {
      const session = sessionIn(s.workspaces, id);
      if (session) session.state = sessionState;
    });
  }

  // retry repeats a request each second until it is answered, showing each
  // failure as the page error meanwhile. It returns undefined once the stream
  // its request is sent on ends, which the page reports itself.
  async function retry<T>(request: () => Promise<T>): Promise<T | undefined> {
    let failure: Error | undefined;
    for (;;) {
      try {
        const answer = await request();
        if (failure !== undefined && error() === failure) setError(undefined);
        return answer;
      } catch (e) {
        if (e instanceof Disconnected) return undefined;
        failure = e as Error;
        setError(failure);
        await new Promise((resolve) => setTimeout(resolve, 1000));
      }
    }
  }

  // shown is the session the page shows, which is the only one it loads. Each
  // show makes a new value, and a load ends once its value is no longer shown.
  let shown: { id: string } | undefined;

  // show records the session the page shows and loads it. The session shown
  // before drops its snapshot.
  function show(id: string | undefined) {
    if (id === shown?.id) return;
    const previous = shown;
    shown = id === undefined ? undefined : { id };
    setStore((s) => {
      const old = previous && sessionIn(s.workspaces, previous.id);
      if (old) old.load = { kind: "unloaded" };
    });
    const current = live();
    if (shown && current) begin(shown, current.rpc);
  }

  // begin loads a listed session that has no snapshot yet.
  function begin(view: { id: string }, rpc: Rpc) {
    const session = stored(view.id);
    if (!session) return;
    if (session.load.kind === "unloaded") {
      setStore((s) => {
        const loaded = sessionIn(s.workspaces, view.id);
        if (loaded) loaded.load = { kind: "loading" };
      });
    }
    void load(view, rpc);
  }

  // load requests a shown session's snapshot each second, keeping each
  // failure in the session's load, until it is answered, the page shows
  // another session, or the stream ends. It installs the snapshot without
  // replacing the session's view or its open tool details. Live updates start
  // at the session/get response boundary.
  async function load(view: { id: string }, rpc: Rpc) {
    const { id } = view;
    let result: Methods["session/get"][1];
    for (;;) {
      if (view !== shown) return;
      try {
        result = await rpc("session/get", { session_id: id });
        break;
      } catch (e) {
        if (e instanceof Disconnected || (e instanceof RpcError && e.code === notFound)) return;
        if (view !== shown) return;
        const failure = (e as Error).message;
        setStore((s) => {
          const loaded = sessionIn(s.workspaces, id);
          if (loaded?.load.kind === "loading") loaded.load.failure = failure;
        });
        await new Promise((resolve) => setTimeout(resolve, 1000));
      }
    }
    if (view !== shown) return;
    const snapshot = snapshotOf(result);
    setStore((s) => {
      const loaded = sessionIn(s.workspaces, id);
      if (!loaded) return;
      const previous = loaded.load.kind === "unloaded" ? undefined : loaded.load.snapshot;
      if (previous) {
        previous.settings = snapshot.settings;
        previous.queued = snapshot.queued;
        previous.usage = snapshot.usage;
        reconcile(snapshot.entries, null)(previous.entries);
        reconcile(snapshot.approvals, "id")(previous.approvals);
      }
      loaded.load = { kind: "live", snapshot: previous ?? snapshot };
    });
    controls.get(id)?.reconcilePrompt(snapshot);
    controls.get(id)?.reconcileApprovals(snapshot);
  }

  // A new oxd connection has missed every notification sent while the page
  // was disconnected, so it reloads the lists and the shown session. A reload
  // belongs to the stream that started it: it sends only through that
  // stream's rpc, and ends when that stream does.
  async function reload(rpc: Rpc) {
    setError(undefined);
    const harnessList = await retry(() => rpc("harness/list", {}));
    if (!harnessList) return;
    const workspaceList = await retry(() => rpc("workspace/list", {}));
    if (!workspaceList) return;
    const { harnesses, permission_modes, defaults } = harnessList;
    const { workspaces } = workspaceList;
    const listed = workspaces.flatMap((w) => w.sessions);
    const ids = new Set(listed.map((s) => s.session_id));
    setStore((s) => {
      s.harnesses = harnesses;
      s.permission_modes = permission_modes;
      s.defaults = defaults;
      const previous = new Map(
        s.workspaces.flatMap((w) => w.sessions.map((session) => [session.id, session] as const)),
      );
      for (const id of previous.keys()) {
        if (!ids.has(id)) controls.delete(id);
      }
      s.workspaces = workspaces.map((w) => ({
        id: w.workspace_id,
        path: w.path,
        sessions: w.sessions.map(
          ({ session_id: id, label, harness, directory, worktree, child, archived = false, pinned = false, state }) => {
            const session = previous.get(id);
            if (!session)
              return {
                id,
                harness,
                directory,
                worktree,
                child,
                label,
                archived,
                pinned,
                state,
                load: { kind: "unloaded" },
              };
            session.label = label;
            session.archived = archived;
            session.pinned = pinned;
            return session;
          },
        ),
      }));
    });
    for (const { session_id, state } of listed) setState(session_id, state);
    setLive({ rpc });
    if (shown && ids.has(shown.id)) begin(shown, rpc);
  }

  function receive({ method, params }: Notification) {
    switch (method) {
      case "session/update": {
        if (params.type === "state") setState(params.session_id, params.state);
        else if (params.type === "parked") setState(params.session_id, "parked");
        if (stored(params.session_id)?.load.kind !== "live") return;
        if (params.type === "prompt" || params.type === "prompt_cancelled") {
          controls.get(params.session_id)?.finishPrompt(params.message_id, params.type === "prompt_cancelled");
        }
        if (params.type === "approval_answered" || params.type === "approval_cancelled") {
          controls.get(params.session_id)?.endApproval(params.request_id);
        }
        return setStore((s) => {
          const load = sessionIn(s.workspaces, params.session_id)?.load;
          if (load?.kind === "live") apply(load.snapshot, params);
        });
      }
      case "workspace/created":
        return setStore((s) => {
          s.workspaces.push({ id: params.workspace_id, path: params.path, sessions: [] });
        });
      case "workspace/removed":
        return setStore((s) => {
          s.workspaces = s.workspaces.filter((w) => w.id !== params.workspace_id);
        });
      case "session/created": {
        setStore((s) => {
          const workspace = s.workspaces.find((w) => w.id === params.workspace_id);
          workspace?.sessions.push({
            id: params.session_id,
            harness: params.harness,
            directory: params.directory,
            worktree: params.worktree,
            child: params.child,
            label: params.label,
            archived: false,
            pinned: false,
            state: params.state,
            load: { kind: "unloaded" },
          });
        });
        return;
      }
      case "session/configured":
        return setStore((s) => {
          const load = sessionIn(s.workspaces, params.session_id)?.load;
          if (load?.kind === "live") load.snapshot.settings = settingsOf(params);
        });
      case "session/labeled":
        return setStore((s) => {
          const session = sessionIn(s.workspaces, params.session_id);
          if (session) session.label = params.label;
        });
      case "session/archived":
      case "session/unarchived":
        return setStore((s) => {
          const session = sessionIn(s.workspaces, params.session_id);
          if (session) session.archived = method === "session/archived";
        });
      case "session/pinned":
      case "session/unpinned":
        return setStore((s) => {
          const session = sessionIn(s.workspaces, params.session_id);
          if (session) session.pinned = method === "session/pinned";
        });
      case "session/destroyed":
        controls.delete(params.session_id);
        return setStore((s) => {
          const workspace = containing(s.workspaces, params.session_id);
          if (workspace) workspace.sessions = workspace.sessions.filter((s) => s.id !== params.session_id);
        });
    }
  }

  // clock is the page's time in milliseconds. It advances once a second while
  // the page is visible, and paces every request the page repeats.
  const [clock, setClock] = createSignal(Date.now());
  onSettled(() => {
    const interval = setInterval(() => {
      if (document.visibilityState === "visible") setClock(Date.now());
    }, 1000);
    return () => clearInterval(interval);
  });

  onSettled(() => {
    connection = connect({
      onConnect: (rpc) => {
        void reload(rpc);
      },
      onNotification: receive,
      onError: (error) => {
        setLive(undefined);
        setStore((s) => {
          for (const workspace of s.workspaces) {
            for (const session of workspace.sessions) {
              if (session.load.kind !== "unloaded") session.load = { kind: "loading", snapshot: session.load.snapshot };
            }
          }
        });
        setError(error);
      },
    });
    return () => connection.close();
  });

  return {
    state: catalog,
    error,
    ready,
    clock,
    workspaceOf,
    session,
    workspaces,
    sessionsOf,
    archived,
    pinned,
    busy,
    workspaceBusy,
    listsWorkspace,
    addWorkspace: (path: string) => connection.rpc("workspace/create", { path }),
    // removeWorkspace sends a workspace's removal, which shows at once.
    removeWorkspace(id: string) {
      setListing((l) => {
        l.workspaces[id] = "remove";
      });
      return connection.rpc("workspace/remove", { workspace_id: id }).finally(() =>
        setListing((l) => {
          delete l.workspaces[id];
        }),
      );
    },
    listFolder: (root: FileRoot, path: string) => connection.rpc("workspace/list_folder", { ...root, path }),
    readFile: (root: FileRoot, path: string) => connection.rpc("workspace/read_file", { ...root, path }),
    listChanges: (root: FileRoot) => connection.rpc("workspace/list_changes", root),
    show,
    control,
    release,
    destroy: (id: string) => requestFor(id, "destroy"),
    archive: (id: string) => requestFor(id, "archive"),
    unarchive,
    pin: (id: string) => requestFor(id, "pin"),
    unpin: (id: string) => requestFor(id, "unpin"),
  };
}

const OxContext = createContext<ReturnType<typeof createOx>>();

export function OxProvider(props: ParentProps) {
  return <OxContext value={createOx()}>{props.children}</OxContext>;
}

export const useOx = () => useContext(OxContext);
