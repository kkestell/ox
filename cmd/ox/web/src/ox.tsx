import { createContext, createSignal, createStore, onSettled, type ParentProps, reconcile, useContext } from "solid-js";
import { type Connection, connect, RpcError } from "./connection";
import {
  type ApprovalRequest,
  type ApprovalResponse,
  type Defaults,
  type Harness,
  type Methods,
  type Notification,
  notFound,
  type SessionState,
  type Settings,
  type ToolResult,
  type Update,
} from "./protocol";

export type Workspace = { id: string; path: string };

// A session's entries are its conversation, in order. A reply is Claude's
// text up to its next tool call or the end of its turn; a result entry ends a
// turn. A tool is stopped when its process exited before its result.
export type Entry =
  | { kind: "prompt"; text: string }
  | { kind: "reply"; text: string }
  | { kind: "tool"; id: string; name: string; input: Record<string, unknown>; result?: ToolResult; stopped?: boolean }
  | { kind: "result"; result: ToolResult }
  | { kind: "exited"; error: string };

export type Approval = { id: string; request: ApprovalRequest };

export type Session = {
  id: string;
  workspaceId: string;
  harness: string;
  createdAt: string;
  state: SessionState;
  settings: Settings;
  entries: Entry[];
  approvals: Approval[];
  synced: boolean;
};

const settingsOf = (p: Settings): Settings => ({
  model: p.model,
  effort: p.effort,
  permission_mode: p.permission_mode,
});

function stopTools(session: Session) {
  for (const e of session.entries) if (e.kind === "tool" && !e.result) e.stopped = true;
}

function apply(session: Session, u: Update) {
  switch (u.type) {
    case "prompt":
      session.entries.push({ kind: "prompt", text: u.text });
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

// SessionControl is the page's side of one session: its composer text and the
// requests the page has in flight for it, with their errors. It outlives the
// session's view, so leaving the view, or reconnecting, keeps the text and
// repeats no request. A send and a configuration exclude each other, so a
// prompt runs with the settings the page shows. A new session's control
// starts as its draft's composer, and its first send starts before its
// session/get returns.
function createSessionControl(
  id: string,
  rpc: Connection["rpc"],
  session: () => Session | undefined,
  connected: () => boolean,
) {
  const [text, setText] = createSignal("");
  const [sending, setSending] = createSignal(false);
  const [requested, setRequested] = createSignal<Settings>();
  const [error, attempt, clearError] = createAttempt();
  const [notice, setNotice] = createSignal<string>();
  const [creating, setCreating] = createSignal(false);
  const ready = () => connected() && (session()?.synced ?? true);
  // idle is whether the session can take a prompt or a settings change.
  const idle = () => session()?.state === "idle" || session()?.state === "parked";
  const available = () => ready() && !creating() && !sending() && requested() === undefined && idle();
  const start = (request: Promise<unknown>) => {
    setNotice(undefined);
    return attempt(request);
  };
  // post sends a prompt. Text typed while it is pending stays in the box.
  function post(sent: string) {
    setSending(true);
    const request = rpc("session/send", { session_id: id, text: sent }).then(() => {
      if (text() === sent) setText("");
    });
    start(request).finally(() => setSending(false));
  }
  // recover returns to a session whose creation reply was lost, with its
  // current settings, and waits for the user to send the retained text.
  function recover() {
    clearError();
    setNotice("Session recovered. Review its settings and send your prompt.");
  }
  return {
    text,
    setText,
    error,
    // notice is information about the session that is not an error.
    notice,
    creating,
    idle,
    available,
    // requested is the settings a configuration in flight asks for.
    requested,
    // create creates the session with the composer's text as its first
    // prompt, and calls onCreated once the session exists. If an earlier
    // create already reached oxd, it recovers instead of sending.
    create(workspaceId: string, harness: string, settings: Settings, onCreated: () => void) {
      const sent = text();
      if (!ready() || creating() || !sent.trim()) return;
      setCreating(true);
      const request = rpc("session/create", { session_id: id, workspace_id: workspaceId, harness, ...settings }).then(
        (result) => {
          setCreating(false);
          if (result.existing) recover();
          else post(sent);
          onCreated();
        },
      );
      start(request).finally(() => setCreating(false));
    },
    recover,
    send(sent: string) {
      if (available() && sent.trim()) post(sent);
    },
    configure(changes: Partial<Settings>) {
      const current = session()?.settings;
      if (!available() || !current) return;
      const changed = Object.fromEntries(
        Object.entries(changes).filter(([key, value]) => current[key as keyof Settings] !== value),
      ) as Partial<Settings>;
      if (!Object.keys(changed).length) return;
      setRequested({ ...current, ...changed });
      const request = rpc("session/configure", { session_id: id, ...changed });
      start(request).finally(() => setRequested(undefined));
    },
    interrupt: () => start(rpc("session/interrupt", { session_id: id })),
    approve: (requestId: string, response: ApprovalResponse) =>
      start(rpc("session/approve", { session_id: id, request_id: requestId, response })),
  };
}

export type SessionControl = ReturnType<typeof createSessionControl>;

function createOx() {
  const [state, setState] = createStore<{
    harnesses: Record<string, Harness>;
    defaults?: Defaults;
    workspaces: Workspace[];
    sessions: Record<string, Session>;
  }>({
    harnesses: {},
    workspaces: [],
    sessions: {},
  });
  const [error, setError] = createSignal<string>();
  const controls = new Map<string, SessionControl>();
  const [ready, setReady] = createSignal(false);
  let connection!: Connection;

  // control returns the page's control of a session, which may not be loaded
  // yet.
  function control(id: string) {
    let c = controls.get(id);
    if (!c) {
      c = createSessionControl(
        id,
        (method, params) => connection.rpc(method, params),
        () => state.sessions[id],
        ready,
      );
      controls.set(id, c);
    }
    return c;
  }

  // load adds a session, or brings a loaded one up to date in place, which
  // keeps its view and the view's state. Its settings and earlier updates
  // come in the session/get response, and its updates apply from the response
  // onward. A session destroyed before the response is not added; its
  // destroyed event has already arrived.
  async function load(id: string, workspaceId: string, harness: string, createdAt: string) {
    let result: Methods["session/get"][1];
    try {
      result = await connection.rpc("session/get", { session_id: id });
    } catch (e) {
      if (e instanceof RpcError && e.code === notFound) return;
      throw e;
    }
    const session: Session = {
      id,
      workspaceId,
      harness,
      createdAt,
      state: result.state,
      settings: settingsOf(result),
      entries: [],
      approvals: [],
      synced: true,
    };
    for (const u of result.history) apply(session, u);
    // The history's parked updates set the state as they did live; the
    // response's state is the current one.
    session.state = result.state;
    for (const a of result.approvals) session.approvals.push({ id: a.request_id, request: a.request });
    setState((s) => {
      const loaded = s.sessions[id];
      if (!loaded) {
        s.sessions[id] = session;
        return;
      }
      loaded.synced = true;
      loaded.state = session.state;
      loaded.settings = session.settings;
      reconcile(session.entries, null)(loaded.entries);
      reconcile(session.approvals, "id")(loaded.approvals);
    });
  }

  // A new oxd connection has missed every notification sent while the page
  // was disconnected, so it reloads everything.
  async function reload() {
    setError(undefined);
    const { harnesses, defaults } = await connection.rpc("harness/list", {});
    const { workspaces } = await connection.rpc("workspace/list", {});
    const listed = new Set(workspaces.flatMap((w) => w.sessions.map((s) => s.session_id)));
    setState((s) => {
      s.harnesses = harnesses;
      s.defaults = defaults;
      reconcile(
        workspaces.map((w) => ({ id: w.workspace_id, path: w.path })),
        "id",
      )(s.workspaces);
      for (const id of Object.keys(s.sessions)) {
        if (!listed.has(id)) {
          delete s.sessions[id];
          controls.delete(id);
        }
      }
    });
    setReady(true);
    await Promise.all(
      workspaces.flatMap((w) => w.sessions.map((s) => load(s.session_id, w.workspace_id, s.harness, s.created_at))),
    );
  }

  function receive({ method, params }: Notification) {
    switch (method) {
      case "session/update":
        // Updates for a session not loaded yet are in the history it loads with.
        return setState((s) => {
          const session = s.sessions[params.session_id];
          if (session) apply(session, params);
        });
      case "workspace/created":
        return setState((s) => {
          s.workspaces.push({ id: params.workspace_id, path: params.path });
        });
      case "workspace/removed":
        return setState((s) => {
          s.workspaces = s.workspaces.filter((w) => w.id !== params.workspace_id);
        });
      case "session/created":
        return void load(params.session_id, params.workspace_id, params.harness, params.created_at).catch(fail);
      case "session/configured":
        return setState((s) => {
          const session = s.sessions[params.session_id];
          if (session) session.settings = settingsOf(params);
        });
      case "session/destroyed":
        controls.delete(params.session_id);
        return setState((s) => {
          delete s.sessions[params.session_id];
        });
    }
  }

  const fail = (e: Error) => setError(e.message);

  onSettled(() => {
    connection = connect({
      onConnect: () => {
        void reload().catch(fail);
      },
      onNotification: receive,
      onError: (error) => {
        setReady(false);
        setState((s) => {
          for (const session of Object.values(s.sessions)) session.synced = false;
        });
        fail(error);
      },
    });
    return () => connection.close();
  });

  return {
    state,
    error,
    ready,
    addWorkspace: (path: string) => connection.rpc("workspace/create", { path }),
    removeWorkspace: (id: string) => connection.rpc("workspace/remove", { workspace_id: id }),
    control,
    destroy: (id: string) => connection.rpc("session/destroy", { session_id: id }),
  };
}

const OxContext = createContext<ReturnType<typeof createOx>>();

export function OxProvider(props: ParentProps) {
  return <OxContext value={createOx()}>{props.children}</OxContext>;
}

export const useOx = () => useContext(OxContext);
