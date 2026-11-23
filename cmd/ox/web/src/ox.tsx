import {
  createContext,
  createSignal,
  createStore,
  onSettled,
  type ParentProps,
  reconcile,
  untrack,
  useContext,
} from "solid-js";
import { type Connection, connect, Disconnected, RpcError } from "./connection";
import {
  type ApprovalRequest,
  type ApprovalResponse,
  type Child,
  type Defaults,
  type Harness,
  type Notification,
  notFound,
  type Prompt,
  type SessionState,
  type Settings,
  type SettingsPatch,
  settled,
  type ToolResult,
  type Update,
} from "./protocol";

export type Workspace = { id: string; path: string; sessions: Session[] };

// A session's entries are its conversation, in order. A message is a prompt
// from another session. A reply is Claude's
// text up to its next tool call or the end of its turn; a result entry ends a
// turn. A tool is stopped when its turn ended or its process exited before its result.
export type Entry =
  | { kind: "prompt"; input: Prompt }
  | { kind: "reply"; text: string }
  | { kind: "tool"; id: string; name: string; input: Record<string, unknown>; result?: ToolResult; stopped?: boolean }
  | { kind: "result"; result: ToolResult }
  | { kind: "exited"; error: string };

export type Approval = { id: string; request: ApprovalRequest };

export type Session = {
  id: string;
  harness: string;
  child?: Child;
  // label is the session's label, as oxd lists and announces it.
  label: string;
  archived: boolean;
  load: { kind: "reloading"; snapshot?: SessionSnapshot } | { kind: "live"; snapshot: SessionSnapshot };
};

export type SessionSnapshot = {
  state: SessionState;
  settings: Settings;
  entries: Entry[];
  approvals: Approval[];
  queued: Prompt[];
};

const settingsOf = (p: Settings): Settings => ({
  model: p.model,
  effort: p.effort,
  permission_mode: p.permission_mode,
});

// workspaceName is the name of a workspace's directory.
export const workspaceName = (path: string) => path.split("/").at(-1) || path;

function stopTools(session: SessionSnapshot) {
  for (const e of session.entries) if (e.kind === "tool" && !e.result) e.stopped = true;
}

function apply(session: SessionSnapshot, u: Update) {
  switch (u.type) {
    case "prompt":
      session.entries.push({ kind: "prompt", input: u });
      session.queued = session.queued.filter((p) => p.message_id !== u.message_id);
      break;
    case "prompt_queued":
      session.queued.push(u);
      break;
    case "prompt_cancelled":
      session.queued = session.queued.filter((p) => p.message_id !== u.message_id);
      break;
    case "text_delta": {
      const last = session.entries.at(-1);
      if (last?.kind === "reply") last.text += u.text;
      else session.entries.push({ kind: "reply", text: u.text });
      break;
    }
    case "tool_use":
      session.entries.push({ kind: "tool", id: u.tool_use_id, name: u.tool_name, input: u.input });
      break;
    case "tool_result": {
      const tool = session.entries.findLast((e) => e.kind === "tool" && e.id === u.tool_use_id);
      if (tool?.kind === "tool") tool.result = u.result;
      break;
    }
    case "result":
      session.entries.push({ kind: "result", result: u.result });
      stopTools(session);
      break;
    case "state":
      session.state = u.state;
      break;
    case "parked":
      session.state = "parked";
      stopTools(session);
      break;
    case "approval":
      session.approvals.push({ id: u.request_id, request: u.request });
      break;
    case "approval_answered":
    case "approval_cancelled": {
      const i = session.approvals.findIndex((a) => a.id === u.request_id);
      if (i >= 0) session.approvals.splice(i, 1);
      break;
    }
    case "exited":
      session.entries.push({ kind: "exited", error: u.error });
      stopTools(session);
      break;
  }
}

// createAttempt runs requests and holds an error until the next request
// starts or clear runs. A request that fails shows its error even after a
// later one has started. The promise attempt returns settles once the request
// has.
export function createAttempt() {
  const [error, setError] = createSignal<string>();
  const clear = () => setError(undefined);
  const attempt = (request: Promise<unknown>) => {
    clear();
    return request.then(
      () => {},
      (e: Error) => setError(e.message),
    );
  };
  return [error, attempt, clear] as const;
}

// SessionControl is the page's side of one session: its composer text, the
// folders its file tree has expanded, its open file, and the requests the page
// has in flight for it, with their errors. It outlives the session's view, so
// leaving the view, or reconnecting, keeps the text, expanded folders, and open
// file and repeats no request. A send and a configuration exclude each other, so a
// prompt runs with the settings the page shows. A new session's control
// starts as its draft's composer, and its first send starts before its
// session/get returns. The control remembers the prompt it sent last, so a
// prompt dropped before Claude took it returns to this page's composer.
function createSessionControl(
  id: string,
  rpc: Connection["rpc"],
  session: () => Session | undefined,
  connected: () => boolean,
) {
  const [text, writeText] = createSignal("");
  // edits counts every write to the text, so a send can tell whether the box
  // changed since its submit.
  let edits = 0;
  const setText = (next: string | ((draft: string) => string)) => {
    edits++;
    writeText(next);
  };
  const [sending, setSending] = createSignal(false);
  const [requested, setRequested] = createSignal<SettingsPatch>();
  const [error, attempt, clearError] = createAttempt();
  const [notice, setNotice] = createSignal<string>();
  // creation is the state of this control's session/create. "unconfirmed"
  // means a create was sent and oxd did not confirm it as new: its request
  // failed, or oxd answered that the session already existed.
  const [creation, setCreation] = createSignal<"none" | "creating" | "unconfirmed">("none");
  // expanded holds the paths of the expanded folders in the file tree.
  const [expanded, setExpandedStore] = createStore<Record<string, true>>({});
  // file is the path of the file open in the file viewer, and split the
  // transcript's share of the width it shares with the viewer. The layout
  // renders the share within each side's minimum width without changing it.
  const [file, setFile] = createSignal<string>();
  const [split, writeSplit] = createSignal(0.5);
  // Keep a successfully sent prompt only until Claude takes it or it drops.
  let sent: { id: string; text: string } | undefined;
  const live = () => {
    const load = session()?.load;
    return load?.kind === "live" ? load.snapshot : undefined;
  };
  const free = () => connected() && creation() !== "creating" && !sending() && requested() === undefined;
  const canSend = () => free() && live()?.queued.length === 0;
  const canConfigure = () => {
    const state = live()?.state;
    return free() && state !== undefined && settled(state);
  };

  function finishPrompt(id: string, dropped: boolean) {
    if (sent?.id !== id) return;
    const { text: prompt } = sent;
    sent = undefined;
    if (dropped) setText((draft) => (draft ? `${prompt}\n${draft}` : prompt));
  }

  function reconcilePrompt(snapshot: SessionSnapshot) {
    const id = sent?.id;
    if (id === undefined || snapshot.queued.some((p) => p.message_id === id)) return;
    finishPrompt(id, !snapshot.entries.some((e) => e.kind === "prompt" && e.input.message_id === id));
  }
  const start = (request: Promise<unknown>) => {
    setNotice(undefined);
    return attempt(request);
  };
  // post sends a prompt, submitted when the text had the given edit count.
  // Text changed since the submit stays in the box.
  function post(prompt: string, submitted: number) {
    setSending(true);
    const request = rpc("session/send", { session_id: id, text: prompt }).then((result) => {
      sent = { id: result.message_id, text: prompt };
      if (edits === submitted) setText("");
      // The daemon publishes the input before replying, so a live snapshot
      // already knows whether it was queued, taken, or dropped.
      const snapshot = live();
      if (snapshot) reconcilePrompt(snapshot);
    });
    start(request).finally(() => setSending(false));
  }
  // recover returns to a session whose creation was unconfirmed, with its
  // current settings, and waits for the user to send the retained text.
  function recover() {
    setCreation("none");
    clearError();
    setNotice("Session recovered. Review its settings and send your prompt.");
  }
  return {
    text,
    setText,
    error,
    // notice is information about the session that is not an error.
    notice,
    // canCreate is whether a draft can submit its first prompt.
    canCreate: free,
    // recoverable is whether the session exists although its creation was
    // unconfirmed, so the draft returns to it instead of sending.
    recoverable: () => creation() === "unconfirmed" && live() !== undefined,
    canSend,
    canConfigure,
    // requested is the settings a configuration in flight asks for.
    requested,
    isExpanded: (path: string) => expanded[path] === true,
    setExpanded(path: string, open: boolean) {
      setExpandedStore((folders) => {
        if (open) folders[path] = true;
        else delete folders[path];
      });
    },
    file,
    setFile: (path: string | undefined) => setFile(path),
    split,
    // setSplit sets the transcript's share of the width it shares with the
    // viewer.
    setSplit: (share: number) => writeSplit(share),
    // create creates the session with the composer's text as its first
    // prompt, and calls onCreated once oxd confirms the session as new.
    // Otherwise the creation is unconfirmed, and nothing is sent.
    create(workspaceId: string, harness: string, settings: Settings, onCreated: () => void) {
      const prompt = text();
      const submitted = edits;
      if (!free() || !prompt.trim()) return;
      setCreation("creating");
      const { effort, ...required } = settings;
      const request = rpc("session/create", {
        session_id: id,
        workspace_id: workspaceId,
        harness,
        ...required,
        ...(effort ? { effort } : {}),
      }).then(
        (result) => {
          if (result.existing) {
            setCreation("unconfirmed");
            return;
          }
          setCreation("none");
          post(prompt, submitted);
          onCreated();
        },
        (e: Error) => {
          setCreation("unconfirmed");
          throw e;
        },
      );
      start(request);
    },
    recover,
    send(prompt: string) {
      if (canSend() && prompt.trim()) post(prompt, edits);
    },
    finishPrompt,
    reconcilePrompt,
    configure(changes: SettingsPatch) {
      const current = live()?.settings;
      if (!canConfigure() || !current) return;
      const changed = Object.entries(changes).filter(
        ([key, value]) => value !== undefined && current[key as keyof Settings] !== value,
      );
      if (!changed.length) return;
      const patch = Object.fromEntries(changed) as SettingsPatch;
      setRequested(patch);
      const request = rpc("session/configure", { session_id: id, ...patch });
      start(request).finally(() => setRequested(undefined));
    },
    interrupt: () => start(rpc("session/interrupt", { session_id: id })),
    approve: (requestId: string, response: ApprovalResponse) =>
      start(rpc("session/approve", { session_id: id, request_id: requestId, response })),
    unarchive: () => start(rpc("session/unarchive", { session_id: id })),
  };
}

export type SessionControl = ReturnType<typeof createSessionControl>;

function createOx() {
  const [state, setState] = createStore<{
    harnesses: Record<string, Harness>;
    defaults?: Defaults;
    workspaces: Workspace[];
  }>({
    harnesses: {},
    workspaces: [],
  });
  const [error, setError] = createSignal<string>();
  const controls = new Map<string, SessionControl>();
  const [ready, setReady] = createSignal(false);
  let connection!: Connection;

  const workspaceOf = (id: string) => containing(state.workspaces, id);
  const containing = (workspaces: Workspace[], id: string) =>
    workspaces.find((w) => w.sessions.some((s) => s.id === id));
  const sessionIn = (workspaces: Workspace[], id: string) =>
    containing(workspaces, id)?.sessions.find((s) => s.id === id);
  const session = (id: string) => sessionIn(state.workspaces, id);

  // control returns the page's control of a session, which may not be loaded
  // yet.
  function control(id: string) {
    let c = controls.get(id);
    if (!c) {
      c = createSessionControl(
        id,
        (method, params) => connection.rpc(method, params),
        () => session(id),
        ready,
      );
      controls.set(id, c);
    }
    return c;
  }

  // release forgets the page's control of a session that will never exist.
  const release = (id: string) => controls.delete(id);

  // retry repeats a request each second until it is answered, showing each
  // failure as the page error meanwhile. It returns undefined once the stream
  // is lost, which the page reports itself.
  async function retry<T>(request: () => Promise<T>): Promise<T | undefined> {
    let shown: string | undefined;
    for (;;) {
      try {
        const answer = await request();
        if (shown !== undefined && error() === shown) setError(undefined);
        return answer;
      } catch (e) {
        if (e instanceof Disconnected) return undefined;
        shown = (e as Error).message;
        setError(shown);
        await new Promise((resolve) => setTimeout(resolve, 1000));
      }
    }
  }

  // Install the snapshot without replacing the session's view or its open
  // tool details. Live updates start at the session/get response boundary.
  async function load(id: string) {
    const result = await retry(async () => {
      try {
        return await connection.rpc("session/get", { session_id: id });
      } catch (e) {
        if (e instanceof RpcError && e.code === notFound) return undefined;
        throw e;
      }
    });
    if (!result) return;
    const snapshot: SessionSnapshot = {
      state: result.state,
      settings: settingsOf(result),
      entries: [],
      approvals: [],
      queued: [],
    };
    for (const u of result.history) apply(snapshot, u);
    // History can include old parked updates. The response has current state.
    snapshot.state = result.state;
    snapshot.approvals = result.approvals.map((a) => ({ id: a.request_id, request: a.request }));
    snapshot.queued = result.queued;
    setState((s) => {
      const loaded = sessionIn(s.workspaces, id);
      if (!loaded) return;
      const previous = loaded.load.snapshot;
      if (previous) {
        previous.state = snapshot.state;
        previous.settings = snapshot.settings;
        previous.queued = snapshot.queued;
        reconcile(snapshot.entries, null)(previous.entries);
        reconcile(snapshot.approvals, "id")(previous.approvals);
      }
      loaded.load = { kind: "live", snapshot: previous ?? snapshot };
    });
    controls.get(id)?.reconcilePrompt(snapshot);
  }

  // A new oxd connection has missed every notification sent while the page
  // was disconnected, so it reloads everything.
  async function reload() {
    setError(undefined);
    const harnessList = await retry(() => connection.rpc("harness/list", {}));
    if (!harnessList) return;
    const workspaceList = await retry(() => connection.rpc("workspace/list", {}));
    if (!workspaceList) return;
    const { harnesses, defaults } = harnessList;
    const { workspaces } = workspaceList;
    const listed = new Set(workspaces.flatMap((w) => w.sessions.map((s) => s.session_id)));
    setState((s) => {
      s.harnesses = harnesses;
      s.defaults = defaults;
      const previous = new Map(
        s.workspaces.flatMap((w) => w.sessions.map((session) => [session.id, session] as const)),
      );
      for (const id of previous.keys()) {
        if (!listed.has(id)) controls.delete(id);
      }
      s.workspaces = workspaces.map((w) => ({
        id: w.workspace_id,
        path: w.path,
        sessions: w.sessions.map(({ session_id: id, label, harness, child, archived = false }) => {
          const session = previous.get(id);
          if (!session) return { id, harness, child, label, archived, load: { kind: "reloading" } };
          session.label = label;
          session.archived = archived;
          return session;
        }),
      }));
    });
    setReady(true);
    await Promise.all([...listed].map(load));
  }

  function receive({ method, params }: Notification) {
    switch (method) {
      case "session/update": {
        if (session(params.session_id)?.load.kind !== "live") return;
        if (params.type === "prompt" || params.type === "prompt_cancelled") {
          controls.get(params.session_id)?.finishPrompt(params.message_id, params.type === "prompt_cancelled");
        }
        return setState((s) => {
          const load = sessionIn(s.workspaces, params.session_id)?.load;
          if (load?.kind === "live") apply(load.snapshot, params);
        });
      }
      case "workspace/created":
        return setState((s) => {
          s.workspaces.push({ id: params.workspace_id, path: params.path, sessions: [] });
        });
      case "workspace/removed":
        return setState((s) => {
          s.workspaces = s.workspaces.filter((w) => w.id !== params.workspace_id);
        });
      case "session/created":
        setState((s) => {
          const workspace = s.workspaces.find((w) => w.id === params.workspace_id);
          workspace?.sessions.push({
            id: params.session_id,
            harness: params.harness,
            child: params.child,
            label: params.label,
            archived: false,
            load: { kind: "reloading" },
          });
        });
        if (ready()) void load(params.session_id);
        return;
      case "session/configured":
        return setState((s) => {
          const load = sessionIn(s.workspaces, params.session_id)?.load;
          if (load?.kind === "live") load.snapshot.settings = settingsOf(params);
        });
      case "session/labeled":
        return setState((s) => {
          const session = sessionIn(s.workspaces, params.session_id);
          if (session) session.label = params.label;
        });
      case "session/archived":
      case "session/unarchived":
        return setState((s) => {
          const session = sessionIn(s.workspaces, params.session_id);
          if (session) session.archived = method === "session/archived";
        });
      case "session/destroyed":
        controls.delete(params.session_id);
        return setState((s) => {
          const workspace = containing(s.workspaces, params.session_id);
          if (workspace) workspace.sessions = workspace.sessions.filter((s) => s.id !== params.session_id);
        });
    }
  }

  // clock advances once a second while the page is visible and connected, and
  // paces every request the page repeats.
  const [clock, setClock] = createSignal(0);
  onSettled(() => {
    const interval = setInterval(() => {
      if (document.visibilityState === "visible" && untrack(ready)) setClock((n) => n + 1);
    }, 1000);
    return () => clearInterval(interval);
  });

  onSettled(() => {
    connection = connect({
      onConnect: () => {
        void reload();
      },
      onNotification: receive,
      onError: (error) => {
        setReady(false);
        setState((s) => {
          for (const workspace of s.workspaces) {
            for (const session of workspace.sessions) {
              session.load = { kind: "reloading", snapshot: session.load.snapshot };
            }
          }
        });
        setError(error.message);
      },
    });
    return () => connection.close();
  });

  return {
    state,
    error,
    ready,
    clock,
    workspaceOf,
    session,
    addWorkspace: (path: string) => connection.rpc("workspace/create", { path }),
    removeWorkspace: (id: string) => connection.rpc("workspace/remove", { workspace_id: id }),
    listFolder: (workspaceId: string, path: string) =>
      connection.rpc("workspace/list_folder", { workspace_id: workspaceId, path }),
    readFile: (workspaceId: string, path: string) =>
      connection.rpc("workspace/read_file", { workspace_id: workspaceId, path }),
    control,
    release,
    destroy: (id: string) => connection.rpc("session/destroy", { session_id: id }),
    archive: (id: string) => connection.rpc("session/archive", { session_id: id }),
    unarchive: (id: string) => connection.rpc("session/unarchive", { session_id: id }),
  };
}

const OxContext = createContext<ReturnType<typeof createOx>>();

export function OxProvider(props: ParentProps) {
  return <OxContext value={createOx()}>{props.children}</OxContext>;
}

export const useOx = () => useContext(OxContext);
