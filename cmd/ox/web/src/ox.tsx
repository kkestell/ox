import { createContext, createSignal, createStore, onSettled, type ParentProps, reconcile, useContext } from "solid-js";
import { type Connection, connect, Disconnected, type Rpc, RpcError } from "./connection";
import { languageFor } from "./highlight";
import { newID } from "./id";
import {
  type ApprovalRequest,
  type ApprovalResponse,
  type Child,
  type Defaults,
  type FileRoot,
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
  type UsageWindow,
} from "./protocol";

export type Workspace = { id: string; path: string; sessions: Session[] };

// A session's entries are its conversation, in order. A message is a prompt
// from another session. A reply is Claude's
// text up to its next tool call or the end of its turn; a result entry ends a
// turn. A tool is stopped when its turn ended or its process exited before its result.
export type Entry =
  | { kind: "prompt"; input: Prompt }
  | { kind: "reply"; text: string }
  | { kind: "reasoning"; text: string }
  | { kind: "tool"; id: string; name: string; input: Record<string, unknown>; result?: ToolResult; stopped?: boolean }
  | { kind: "result"; result: ToolResult }
  | { kind: "exited"; error: string };

export type Approval = { id: string; request: ApprovalRequest };

export type MarkdownView = "preview" | "source";

// OpenFile is the file open in the file viewer. Only a Markdown file has a
// view, which shows it rendered or as its source.
export type OpenFile = { path: string; view?: MarkdownView };

export type Session = {
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
  // state is the state oxd last published for the session, which the page
  // follows whether or not the session is loaded.
  state: SessionState;
  load: { kind: "reloading"; snapshot?: SessionSnapshot } | { kind: "live"; snapshot: SessionSnapshot };
};

export type SessionSnapshot = {
  settings: Settings;
  entries: Entry[];
  approvals: Approval[];
  queued: Prompt[];
  // usage is the windows of the session's latest usage update, shortest first.
  usage?: UsageWindow[];
};

const settingsOf = (p: Settings): Settings => ({
  model: p.model,
  effort: p.effort,
  permission_mode: p.permission_mode,
});

// directoryName is the name of a directory.
export const directoryName = (path: string) => path.replace(/\/+$/, "").split("/").at(-1) || path;

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
    case "reasoning_delta": {
      const last = session.entries.at(-1);
      if (last?.kind === "reasoning") last.text += u.text;
      else session.entries.push({ kind: "reasoning", text: u.text });
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
    case "parked":
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
    case "usage":
      session.usage = u.usage;
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

// Outgoing is the composer's prompt the page has taken for a session/create
// or session/send. It is "creating" or "sending" while that request is in
// flight, and "sent" once oxd accepted it, until Claude takes it or it drops.
export type Outgoing = { id: string; text: string; images: File[]; phase: "creating" | "sending" | "sent" };

// imageTypes are the image types a prompt takes.
export const imageTypes = ["image/png", "image/jpeg", "image/gif", "image/webp"];

// maxImageBytes is the most image data one prompt takes.
const maxImageBytes = 10 << 20;

// base64 reads a file as base64.
const base64 = (file: File) =>
  new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const url = reader.result as string;
      resolve(url.slice(url.indexOf(",") + 1));
    };
    reader.onerror = () => reject(reader.error ?? new Error(`could not read ${file.name}`));
    reader.readAsDataURL(file);
  });

// SessionControl is the page's side of one session: its composer text and
// attached images, the folders its file tree has expanded, its open file, and
// the requests the page has in flight for it, with their errors. It outlives
// the session's view, so leaving the view, or reconnecting, keeps the text,
// images, expanded folders, and open file and repeats no request. A send and a
// configuration exclude each other, so a prompt runs with the settings the
// page shows. A new session's control starts as its draft's composer, and its
// first send starts before its session/get returns. Submitting takes the
// prompt out of the composer at once; a prompt oxd refuses, or one dropped
// before Claude took it, returns to this page's composer. busy is whether the
// session or an ancestor has a session list request in flight, and unarchive
// sends this session's.
function createSessionControl(
  id: string,
  rpc: Connection["rpc"],
  session: () => Session | undefined,
  connected: () => boolean,
  busy: () => boolean,
  unarchive: () => Promise<unknown>,
) {
  const [text, setText] = createSignal("");
  const [images, setImages] = createSignal<File[]>([]);
  const [outgoing, setOutgoing] = createSignal<Outgoing>();
  // stopping is set by a Stop and lasts until the session leaves running or
  // oxd refuses the Stop.
  const [stopping, setStopping] = createSignal(false);
  // answering holds the approvals whose answers are in flight.
  const [answering, setAnswering] = createSignal<ReadonlySet<string>>(new Set());
  const [requested, setRequested] = createSignal<SettingsPatch>();
  const [error, attempt, clearError] = createAttempt();
  const [notice, setNotice] = createSignal<string>();
  // unconfirmed means a create was sent and oxd did not confirm it as new:
  // its request failed, or oxd answered that the session already existed.
  const [unconfirmed, setUnconfirmed] = createSignal(false);
  // expanded holds the paths of the expanded folders in the file tree.
  const [expanded, setExpandedStore] = createStore<Record<string, true>>({});
  // file is the file open in the file viewer, with its Markdown view, and
  // split the transcript's share of the width it shares with the viewer. The
  // layout renders the share within each side's minimum width without
  // changing it.
  const [file, writeFile] = createSignal<OpenFile>();
  const [split, writeSplit] = createSignal(0.5);
  const live = () => {
    const load = session()?.load;
    return load?.kind === "live" ? load.snapshot : undefined;
  };
  // submitting is whether the composer's prompt is in a request.
  const submitting = () => {
    const phase = outgoing()?.phase;
    return phase === "creating" || phase === "sending";
  };
  const free = () => connected() && !submitting() && requested() === undefined && !busy();
  const canSend = () => free() && live()?.queued.length === 0;
  const canConfigure = () => {
    const state = session()?.state;
    return free() && live() !== undefined && state !== undefined && settled(state);
  };

  // restore returns a prompt to the composer, ahead of anything typed since.
  function restore(prompt: Outgoing) {
    if (outgoing()?.id !== prompt.id) return;
    setOutgoing(undefined);
    if (prompt.text) setText((draft) => (draft ? `${prompt.text}\n${draft}` : prompt.text));
    if (prompt.images.length) setImages((draft) => [...prompt.images, ...draft]);
  }

  // finishPrompt ends the outgoing prompt once Claude took it or it dropped,
  // returning a dropped one to the composer.
  function finishPrompt(messageId: string, dropped: boolean) {
    const prompt = outgoing();
    if (prompt?.id !== messageId) return;
    if (dropped) restore(prompt);
    else setOutgoing(undefined);
  }

  // reconcilePrompt finishes the outgoing prompt by a snapshot: taken when its
  // entries hold it, and dropped when oxd accepted it and the snapshot no
  // longer has it.
  function reconcilePrompt(snapshot: SessionSnapshot) {
    const prompt = outgoing();
    if (!prompt || snapshot.queued.some((p) => p.message_id === prompt.id)) return;
    if (snapshot.entries.some((e) => e.kind === "prompt" && e.input.message_id === prompt.id))
      finishPrompt(prompt.id, false);
    else if (prompt.phase === "sent") finishPrompt(prompt.id, true);
  }
  const start = (request: Promise<unknown>) => {
    setNotice(undefined);
    return attempt(request);
  };
  // take moves the composer's text and images into a new outgoing prompt.
  function take(phase: Outgoing["phase"]): Outgoing {
    const prompt: Outgoing = { id: newID(), text: text(), images: images(), phase };
    setOutgoing(prompt);
    setText("");
    setImages([]);
    return prompt;
  }
  // post sends a prompt and its images under the prompt's ID.
  function post(prompt: Outgoing) {
    const sending: Outgoing = { ...prompt, phase: "sending" };
    setOutgoing(sending);
    const request = Promise.all(sending.images.map(base64))
      .then((encoded) =>
        rpc("session/send", {
          session_id: id,
          message_id: sending.id,
          text: sending.text,
          ...(encoded.length ? { images: encoded } : {}),
        }),
      )
      .then(
        () => {
          if (outgoing()?.id !== sending.id) return;
          setOutgoing({ ...sending, phase: "sent" });
          // The daemon publishes the input before replying, so a live snapshot
          // already knows whether it was queued, taken, or dropped.
          const snapshot = live();
          if (snapshot) reconcilePrompt(snapshot);
        },
        (e: Error) => {
          restore(sending);
          throw e;
        },
      );
    start(request);
  }
  // recover returns to a session whose creation was unconfirmed, with its
  // current settings, and waits for the user to send the returned text.
  function recover() {
    setUnconfirmed(false);
    clearError();
    setNotice("Session recovered. Review its settings and send your prompt.");
  }
  // sendable is whether the composer has text or images to send.
  const sendable = () => text().trim() !== "" || images().length > 0;
  return {
    text,
    setText,
    images,
    // attach adds images to the composer, or refuses them all with an error.
    attach(files: File[]) {
      if (!files.length) return;
      if (files.some((f) => !imageTypes.includes(f.type))) {
        start(Promise.reject(new Error("Attach PNG, JPEG, GIF, or WebP images.")));
        return;
      }
      const total = [...images(), ...files].reduce((sum, f) => sum + f.size, 0);
      if (total > maxImageBytes) {
        start(Promise.reject(new Error("Images can total at most 10 MiB.")));
        return;
      }
      setImages((draft) => [...draft, ...files]);
    },
    removeImage: (file: File) => setImages((draft) => draft.filter((f) => f !== file)),
    error,
    // notice is information about the session that is not an error.
    notice,
    outgoing,
    stopping,
    answering,
    // canCreate is whether a draft can submit its first prompt.
    canCreate: free,
    // recoverable is whether the session exists although its creation was
    // unconfirmed, so the draft returns to it instead of sending.
    recoverable: () => unconfirmed() && live() !== undefined,
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
    // setFile opens the file at path, a Markdown file as its preview, or
    // closes the open file. Opening the open file keeps its view.
    setFile(path: string | undefined) {
      if (path === undefined) writeFile(undefined);
      else if (file()?.path !== path)
        writeFile(languageFor(path) === "markdown" ? { path, view: "preview" } : { path });
    },
    // setView switches the open Markdown file between its preview and source.
    setView(view: MarkdownView) {
      const current = file();
      if (current?.view) writeFile({ ...current, view });
    },
    split,
    // setSplit sets the transcript's share of the width it shares with the
    // viewer.
    setSplit: (share: number) => writeSplit(share),
    // create creates the session with the composer's prompt as its first, in
    // a worktree of its own when worktree is set, sends it, and calls
    // onCreated once oxd confirms the session as new.
    // Otherwise the creation is unconfirmed, and the prompt returns to the
    // composer unsent.
    create(workspaceId: string, harness: string, settings: Settings, worktree: boolean, onCreated: () => void) {
      if (!free() || !sendable()) return;
      setUnconfirmed(false);
      const prompt = take("creating");
      const { effort, ...required } = settings;
      const request = rpc("session/create", {
        session_id: id,
        workspace_id: workspaceId,
        harness,
        ...required,
        ...(effort ? { effort } : {}),
        ...(worktree ? { worktree } : {}),
      }).then(
        (result) => {
          if (result.existing) {
            setUnconfirmed(true);
            restore(prompt);
            return;
          }
          post(prompt);
          onCreated();
        },
        (e: Error) => {
          setUnconfirmed(true);
          restore(prompt);
          throw e;
        },
      );
      start(request);
    },
    recover,
    send() {
      if (canSend() && sendable()) post(take("sending"));
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
    interrupt() {
      setStopping(true);
      start(
        rpc("session/interrupt", { session_id: id }).catch((e: Error) => {
          setStopping(false);
          throw e;
        }),
      );
    },
    // stopped ends a Stop once the session has left running.
    stopped: () => setStopping(false),
    approve(requestId: string, response: ApprovalResponse) {
      setAnswering((ids) => new Set(ids).add(requestId));
      start(rpc("session/approve", { session_id: id, request_id: requestId, response })).finally(() =>
        setAnswering((ids) => {
          const next = new Set(ids);
          next.delete(requestId);
          return next;
        }),
      );
    },
    unarchive: () => start(unarchive()),
  };
}

export type SessionControl = ReturnType<typeof createSessionControl>;

// ListRequest is a session list request the page has in flight for a session.
type ListRequest = "archive" | "unarchive" | "destroy";

function createOx() {
  const [state, setStore] = createStore<{
    harnesses: Record<string, Harness>;
    defaults?: Defaults;
    workspaces: Workspace[];
  }>({
    harnesses: {},
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

  const workspaceOf = (id: string) => containing(state.workspaces, id);
  const containing = (workspaces: Workspace[], id: string) =>
    workspaces.find((w) => w.sessions.some((s) => s.id === id));
  const sessionIn = (workspaces: Workspace[], id: string) =>
    containing(workspaces, id)?.sessions.find((s) => s.id === id);
  const stored = (id: string) => sessionIn(state.workspaces, id);
  // lineOf is a session and its ancestors, nearest first.
  function* lineOf(session: Session | undefined) {
    for (let s = session; s; s = s.child && stored(s.child.parent_id)) yield s;
  }

  // The page shows a session list request's result while it is in flight: a
  // session being deleted and a workspace being removed are gone, and a
  // session being archived or unarchived, with every session beneath it,
  // shows as it will be.
  const session = (id: string) => (listing.sessions[id] === "destroy" ? undefined : stored(id));
  const workspaces = () => state.workspaces.filter((w) => listing.workspaces[w.id] === undefined);
  const sessionsOf = (workspace: Workspace) => workspace.sessions.filter((s) => listing.sessions[s.id] !== "destroy");
  const archived = (session: Session) => {
    for (const s of lineOf(session)) {
      const request = listing.sessions[s.id];
      if (request === "archive") return true;
      if (request === "unarchive") return false;
    }
    return session.archived;
  };
  // busy is whether a session or an ancestor has a session list request in
  // flight.
  const busy = (id: string) => [...lineOf(stored(id))].some((s) => listing.sessions[s.id] !== undefined);

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
        () => stored(id),
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
  // list, a session/created, or a state or parked update. A session that
  // leaves running ends the page's Stop.
  function setState(id: string, sessionState: SessionState) {
    setStore((s) => {
      const session = sessionIn(s.workspaces, id);
      if (session) session.state = sessionState;
    });
    if (sessionState !== "running" && sessionState !== "requires_action") controls.get(id)?.stopped();
  }

  // retry repeats a request each second until it is answered, showing each
  // failure as the page error meanwhile. It returns undefined once the stream
  // its request is sent on ends, which the page reports itself.
  async function retry<T>(request: () => Promise<T>): Promise<T | undefined> {
    let shown: Error | undefined;
    for (;;) {
      try {
        const answer = await request();
        if (shown !== undefined && error() === shown) setError(undefined);
        return answer;
      } catch (e) {
        if (e instanceof Disconnected) return undefined;
        shown = e as Error;
        setError(shown);
        await new Promise((resolve) => setTimeout(resolve, 1000));
      }
    }
  }

  // Install the snapshot without replacing the session's view or its open
  // tool details. Live updates start at the session/get response boundary.
  async function load(id: string, rpc: Rpc) {
    const result = await retry(async () => {
      try {
        return await rpc("session/get", { session_id: id });
      } catch (e) {
        if (e instanceof RpcError && e.code === notFound) return undefined;
        throw e;
      }
    });
    if (!result) return;
    const snapshot: SessionSnapshot = {
      settings: settingsOf(result),
      entries: [],
      approvals: [],
      queued: [],
    };
    for (const u of result.history) apply(snapshot, u);
    snapshot.approvals = result.approvals.map((a) => ({ id: a.request_id, request: a.request }));
    snapshot.queued = result.queued;
    setStore((s) => {
      const loaded = sessionIn(s.workspaces, id);
      if (!loaded) return;
      const previous = loaded.load.snapshot;
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
  }

  // A new oxd connection has missed every notification sent while the page
  // was disconnected, so it reloads everything. A reload belongs to the stream
  // that started it: it sends only through that stream's rpc, and ends when
  // that stream does.
  async function reload(rpc: Rpc) {
    setError(undefined);
    const harnessList = await retry(() => rpc("harness/list", {}));
    if (!harnessList) return;
    const workspaceList = await retry(() => rpc("workspace/list", {}));
    if (!workspaceList) return;
    const { harnesses, defaults } = harnessList;
    const { workspaces } = workspaceList;
    const listed = workspaces.flatMap((w) => w.sessions);
    const ids = new Set(listed.map((s) => s.session_id));
    setStore((s) => {
      s.harnesses = harnesses;
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
          ({ session_id: id, label, harness, directory, worktree, child, archived = false, state }) => {
            const session = previous.get(id);
            if (!session)
              return { id, harness, directory, worktree, child, label, archived, state, load: { kind: "reloading" } };
            session.label = label;
            session.archived = archived;
            return session;
          },
        ),
      }));
    });
    for (const { session_id, state } of listed) setState(session_id, state);
    setLive({ rpc });
    await Promise.all([...ids].map((id) => load(id, rpc)));
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
            state: params.state,
            load: { kind: "reloading" },
          });
        });
        const current = live();
        if (current) void load(params.session_id, current.rpc);
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
              session.load = { kind: "reloading", snapshot: session.load.snapshot };
            }
          }
        });
        setError(error);
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
    workspaces,
    sessionsOf,
    archived,
    busy,
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
    control,
    release,
    destroy: (id: string) => requestFor(id, "destroy"),
    archive: (id: string) => requestFor(id, "archive"),
    unarchive,
  };
}

const OxContext = createContext<ReturnType<typeof createOx>>();

export function OxProvider(props: ParentProps) {
  return <OxContext value={createOx()}>{props.children}</OxContext>;
}

export const useOx = () => useContext(OxContext);
