import { createSignal, createStore, type Store, type StoreSetter } from "solid-js";
import { createAttempt } from "./attempt";
import type { Connection } from "./connection";
import { newID } from "./id";
import { type ApprovalResponse, type SessionState, type Settings, type SettingsPatch, settled } from "./protocol";
import type { SessionSnapshot } from "./snapshot";
import { initial as initialLayout, type Layout } from "./tabs";

// Outgoing is the composer's prompt the page has taken for a session/create
// or session/send, from the moment it is taken until Claude takes it, it
// drops, or oxd refuses it.
export type Outgoing = { id: string; text: string; images: File[] };

// Request is the chat request the page has in flight for the session. The page
// sends one at a time, so oxd takes them in the order the user made them.
type Request =
  | { kind: "create" }
  | { kind: "send" }
  | { kind: "configure"; patch: SettingsPatch }
  | { kind: "interrupt" };

// Attention is what the chat shows that needs the user: an error, an approval
// waiting on them, or a notice.
export type Attention = { kind: "error" | "notice"; text: string } | { kind: "waiting" };

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

// CardDraft is a permission card's unsent answer: its choice, and the reply
// typed for Other.
export type CardDraft = { choice?: "allow" | "deny" | "other"; reply: string };

// QuestionAnswer is a question form's unsent answer to one question.
export type QuestionAnswer = { labels: string[]; other: boolean; text: string };

// FormDraft is a question form's unsent answers and the question it shows.
type FormDraft = { answers: QuestionAnswer[]; page: number };

type Draft<T> = [Store<T>, StoreSetter<T>];

// SessionControl is the page's side of one session: its composer text and
// attached images, each pending approval's unsent answers, the folders its
// file tree has expanded, its tabs, and the requests the page has in
// flight for it, with their errors. It outlives the session's view, so leaving
// the view, or reconnecting, keeps the text, images, answers, expanded
// folders, and tabs and repeats no request. A creation, send, configuration,
// and Stop exclude one another, so oxd receives them in the order the user
// made them and a prompt runs with the settings the page shows. A new
// session's control starts as its draft's composer, and its first send starts
// before its session/get returns. Submitting takes the
// prompt out of the composer at once; a prompt oxd refuses, or one dropped
// before Claude took it, returns to this page's composer. busy is whether the
// session or an ancestor has a session list request in flight, and unarchive
// sends this session's.
export function createSessionControl(
  id: string,
  rpc: Connection["rpc"],
  snapshot: () => SessionSnapshot | undefined,
  state: () => SessionState | undefined,
  connected: () => boolean,
  busy: () => boolean,
  unarchive: () => Promise<unknown>,
) {
  const [text, setText] = createSignal("");
  const [images, setImages] = createSignal<File[]>([]);
  const [outgoing, setOutgoing] = createSignal<Outgoing>();
  // request is the creation, send, configuration, or Stop in flight. oxd
  // answers a Stop once the stopped turn has ended.
  const [request, setRequest] = createSignal<Request>();
  // answering holds the approvals whose answers are in flight.
  const [answering, setAnswering] = createSignal<ReadonlySet<string>>(new Set());
  // cardDrafts and formDrafts hold the unsent answers of approvals pending on
  // this page, by approval ID, from the first time a view shows each until it
  // is answered or cancelled.
  const cardDrafts = new Map<string, Draft<CardDraft>>();
  const formDrafts = new Map<string, Draft<FormDraft>>();
  const [error, attempt, clearError, fail] = createAttempt();
  const [notice, setNotice] = createSignal<string>();
  // unconfirmed means a create was sent and oxd did not confirm it as new:
  // its request failed, or oxd answered that the session already existed.
  const [unconfirmed, setUnconfirmed] = createSignal(false);
  // expanded holds the paths of the expanded folders in the file tree.
  const [expanded, setExpandedStore] = createStore<Record<string, true>>({});
  // layout is the session's tabs, their groups, and the groups' sizes.
  const [layout, setLayout] = createSignal<Layout>(initialLayout());
  // scroll is the transcript's scroll offset, which a rebuilt view restores.
  let scroll = 0;
  const live = snapshot;
  // begin puts a request in the slot, and settle clears the slot when the
  // request is still the one in it.
  const begin = (next: Request) => {
    setRequest(next);
    return next;
  };
  const settle = (mine: Request) => {
    if (request() === mine) setRequest(undefined);
  };
  // submitting is whether the outgoing prompt is in a create or send.
  const submitting = () => {
    const kind = request()?.kind;
    return outgoing() !== undefined && (kind === "create" || kind === "send");
  };
  const requested = () => {
    const current = request();
    return current?.kind === "configure" ? current.patch : undefined;
  };
  const free = () => connected() && request() === undefined && !busy();
  const canSend = () => free() && live()?.queued.length === 0;
  const canStop = () => {
    const current = state();
    return free() && (current === "running" || current === "requires_action");
  };
  // waiting is the pending approvals with no answer in flight. One being
  // answered disappears, and returns with its answers if oxd refuses them.
  const waiting = () => live()?.approvals.filter((a) => !answering().has(a.id)) ?? [];
  const attention = (): Attention | undefined => {
    const message = error();
    if (message) return { kind: "error", text: message };
    if (waiting().length) return { kind: "waiting" };
    const text = notice();
    return text ? { kind: "notice", text } : undefined;
  };
  const canConfigure = () => {
    const current = state();
    return free() && live() !== undefined && current !== undefined && settled(current);
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
    else if (!submitting()) finishPrompt(prompt.id, true);
  }
  // endApproval drops an approval's unsent answers once it is no longer
  // pending.
  function endApproval(requestId: string) {
    cardDrafts.delete(requestId);
    formDrafts.delete(requestId);
  }

  // reconcileApprovals drops the unsent answers of every approval a snapshot
  // no longer holds.
  function reconcileApprovals(snapshot: SessionSnapshot) {
    const pending = new Set(snapshot.approvals.map((a) => a.id));
    for (const id of [...cardDrafts.keys(), ...formDrafts.keys()]) {
      if (!pending.has(id)) endApproval(id);
    }
  }
  // refuse shows an error for input the page will not send.
  const refuse = (message: string) => {
    setNotice(undefined);
    fail(message);
  };
  const start = (request: Promise<unknown>) => {
    setNotice(undefined);
    return attempt(request);
  };
  // take moves the composer's text and images into a new outgoing prompt.
  function take(): Outgoing {
    const prompt: Outgoing = { id: newID(), text: text(), images: images() };
    setOutgoing(prompt);
    setText("");
    setImages([]);
    return prompt;
  }
  // post sends a prompt and its images under the prompt's ID.
  function post(prompt: Outgoing) {
    const mine = begin({ kind: "send" });
    const sent = Promise.all(prompt.images.map(base64))
      .then((encoded) =>
        rpc("session/send", {
          session_id: id,
          message_id: prompt.id,
          text: prompt.text,
          ...(encoded.length ? { images: encoded } : {}),
        }),
      )
      .then(
        () => {
          // Settling first makes submitting() false for reconcilePrompt.
          settle(mine);
          // The daemon publishes the input before replying, so a live snapshot
          // already knows whether it was queued, taken, or dropped.
          const snapshot = live();
          if (snapshot) reconcilePrompt(snapshot);
        },
        (e: Error) => {
          restore(prompt);
          settle(mine);
          throw e;
        },
      );
    start(sent);
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
        refuse("Attach PNG, JPEG, GIF, or WebP images.");
        return;
      }
      const total = [...images(), ...files].reduce((sum, f) => sum + f.size, 0);
      if (total > maxImageBytes) {
        refuse("Images can total at most 10 MiB.");
        return;
      }
      setImages((draft) => [...draft, ...files]);
    },
    removeImage: (file: File) => setImages((draft) => draft.filter((f) => f !== file)),
    sendable,
    error,
    // notice is information about the session that is not an error.
    notice,
    outgoing,
    submitting,
    answering,
    waiting,
    attention,
    // cardDraft is a permission card's unsent answer, created empty on first
    // request.
    cardDraft(requestId: string): Draft<CardDraft> {
      let draft = cardDrafts.get(requestId);
      if (!draft) {
        draft = createStore<CardDraft>({ reply: "" });
        cardDrafts.set(requestId, draft);
      }
      return draft;
    },
    // formDraft is a question form's unsent answers, created on first request
    // with an empty answer to each of its questions, on the first question.
    formDraft(requestId: string, questions: number): Draft<FormDraft> {
      let draft = formDrafts.get(requestId);
      if (!draft) {
        const answers = Array.from({ length: questions }, () => ({ labels: [], other: false, text: "" }));
        draft = createStore<FormDraft>({ answers, page: 0 });
        formDrafts.set(requestId, draft);
      }
      return draft;
    },
    endApproval,
    reconcileApprovals,
    // canCreate is whether a draft can submit its first prompt.
    canCreate: free,
    // recoverable is whether the session exists although its creation was
    // unconfirmed, so the draft returns to it instead of sending.
    recoverable: () => unconfirmed() && state() !== undefined,
    canSend,
    canStop,
    canConfigure,
    // requested is the settings a configuration in flight asks for.
    requested,
    // creating is whether the page's session/create is in flight.
    creating: () => request()?.kind === "create",
    isExpanded: (path: string) => expanded[path] === true,
    setExpanded(path: string, open: boolean) {
      setExpandedStore((folders) => {
        if (open) folders[path] = true;
        else delete folders[path];
      });
    },
    scroll: () => scroll,
    setScroll: (offset: number) => {
      scroll = offset;
    },
    layout,
    // edit replaces the layout with the result of an operation on it.
    edit: (change: (current: Layout) => Layout) => setLayout((current) => change(current)),
    // create creates the session with the composer's prompt as its first, in
    // a worktree of its own when worktree is set, sends it, and calls
    // onCreated once oxd confirms the session as new.
    // Otherwise the creation is unconfirmed, and the prompt returns to the
    // composer unsent.
    create(workspaceId: string, harness: string, settings: Settings, worktree: boolean, onCreated: () => void) {
      if (!free() || !sendable()) return;
      setUnconfirmed(false);
      const prompt = take();
      const mine = begin({ kind: "create" });
      const { effort, ...required } = settings;
      const created = rpc("session/create", {
        session_id: id,
        workspace_id: workspaceId,
        harness,
        ...required,
        ...(effort ? { effort } : {}),
        ...(worktree ? { worktree } : {}),
      })
        .then(
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
        )
        .finally(() => settle(mine));
      start(created);
    },
    recover,
    send() {
      if (canSend() && sendable()) post(take());
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
      const mine = begin({ kind: "configure", patch });
      start(rpc("session/configure", { session_id: id, ...patch }).finally(() => settle(mine)));
    },
    interrupt() {
      if (!canStop()) return;
      const mine = begin({ kind: "interrupt" });
      start(rpc("session/interrupt", { session_id: id }).finally(() => settle(mine)));
    },
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
