import { createContext, createSignal, createStore, onSettled, useContext, type ParentProps } from "solid-js";
import { connect, RpcError, type Connection } from "./connection";
import {
  notFound,
  type ApprovalRequest,
  type ApprovalResponse,
  type Defaults,
  type Harness,
  type Notification,
  type SessionState,
  type Settings,
  type ToolResult,
  type Update,
} from "./protocol";

export type Workspace = { id: string; path: string };

// A session's entries are its conversation, in order. A reply is Claude's
// text up to its next tool call or the end of its turn; a result entry ends a
// turn.
export type Entry =
  | { kind: "prompt"; text: string }
  | { kind: "reply"; text: string }
  | { kind: "tool"; id: string; name: string; input: Record<string, unknown>; result?: ToolResult }
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
};

const settingsOf = (p: Settings): Settings => ({ model: p.model, effort: p.effort, permission_mode: p.permission_mode });

// apply adds one update, live or from history, to a session.
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
      break;
  }
}

// createOx holds the page's view of oxd: every workspace and session, kept
// current from the connection's notifications, and the requests that change
// them. Changes reach the view through the notifications oxd sends every
// connection, so the requests write nothing themselves.
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
  let connection!: Connection;

  // load adds a session. Its settings and earlier updates come in the
  // session/get response, and its updates apply from the response onward. A
  // session destroyed before the response is not added; its destroyed event
  // has already arrived.
  async function load(id: string, workspaceId: string, harness: string, createdAt: string) {
    let result;
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
    };
    for (const u of result.history) apply(session, u);
    for (const a of result.approvals) session.approvals.push({ id: a.request_id, request: a.request });
    setState((s) => {
      s.sessions[id] = session;
    });
  }

  // A new oxd connection starts from nothing: it has missed every
  // notification sent while the page was disconnected.
  async function reload() {
    setState((s) => {
      s.workspaces = [];
      s.sessions = {};
    });
    setError(undefined);
    const { harnesses, defaults } = await connection.rpc("harness/list", {});
    const { workspaces } = await connection.rpc("workspace/list", {});
    setState((s) => {
      s.harnesses = harnesses;
      s.defaults = defaults;
      s.workspaces = workspaces.map((w) => ({ id: w.workspace_id, path: w.path }));
    });
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
      case "session/created":
        return void load(params.session_id, params.workspace_id, params.harness, params.created_at).catch(fail);
      case "session/configured":
        return setState((s) => {
          const session = s.sessions[params.session_id];
          if (session) session.settings = settingsOf(params);
        });
      case "session/destroyed":
        return setState((s) => {
          delete s.sessions[params.session_id];
        });
    }
  }

  const fail = (e: Error) => setError(e.message);

  onSettled(() => {
    connection = connect({ onConnect: () => void reload().catch(fail), onNotification: receive });
    return () => connection.close();
  });

  return {
    state,
    error,
    // The created events add the workspace and session to the view. Adding a
    // workspace oxd already has changes nothing.
    addWorkspace: (path: string) => connection.rpc("workspace/create", { path }),
    // startSession creates a session and sends its first prompt.
    async startSession(workspaceId: string, harness: string, settings: Settings, text: string) {
      const { session_id } = await connection.rpc("session/create", { workspace_id: workspaceId, harness, ...settings });
      await connection.rpc("session/send", { session_id, text });
      return session_id;
    },
    send: (id: string, text: string) => connection.rpc("session/send", { session_id: id, text }),
    // configure changes only the settings given.
    configure: (id: string, changes: Partial<Settings>) => connection.rpc("session/configure", { session_id: id, ...changes }),
    approve: (id: string, requestId: string, response: ApprovalResponse) =>
      connection.rpc("session/approve", { session_id: id, request_id: requestId, response }),
    destroy: (id: string) => connection.rpc("session/destroy", { session_id: id }),
  };
}

const OxContext = createContext<ReturnType<typeof createOx>>();

export function OxProvider(props: ParentProps) {
  return <OxContext value={createOx()}>{props.children}</OxContext>;
}

export const useOx = () => useContext(OxContext);
