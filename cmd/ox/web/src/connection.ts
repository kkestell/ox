import { parse, rawJSON, stringify } from "@ungap/raw-json";
import type { Methods, Notification } from "./protocol";

export class RpcError extends Error {
  code: number;
  constructor(message: string, code: number) {
    super(message);
    this.code = code;
  }
}

// Disconnected is a request's failure when the page's connection to oxd,
// through the relay and its event stream, is gone. closed means the relay
// refused the stream, so the page has stopped reconnecting and only a reload
// restores it.
export class Disconnected extends Error {
  readonly closed: boolean;
  constructor(message: string, closed = false) {
    super(message);
    this.closed = closed;
  }
}

export type Rpc = <M extends keyof Methods>(method: M, params: Methods[M][0]) => Promise<Methods[M][1]>;

export type Connection = {
  rpc: Rpc;
  close(): void;
};

export function connect(handlers: {
  // onConnect's rpc sends on the stream that connected, and fails once that
  // stream ends.
  onConnect(rpc: Rpc): void;
  onNotification(notification: Notification): void;
  onError(error: Error): void;
}): Connection {
  const pending = new Map<number, { resolve(result: unknown): void; reject(error: Error): void }>();
  let nextId = 1;

  // The stream's whole lifecycle: waiting holds the timer that reopens a
  // dropped stream, and closed is a refused or closed stream, never reopened.
  type Stream =
    | { kind: "connecting"; source: EventSource }
    | { kind: "connected"; source: EventSource; connection: string }
    | { kind: "waiting"; timer: ReturnType<typeof setTimeout> }
    | { kind: "closed" };
  let stream: Stream = { kind: "closed" };

  const failPending = (error: Error) => {
    for (const call of pending.values()) call.reject(error);
    pending.clear();
  };

  // drop ends the current stream and fails its calls. The page reopens the
  // stream a second later unless the relay refused it.
  const drop = (error: Disconnected) => {
    if (stream.kind === "connecting" || stream.kind === "connected") stream.source.close();
    stream = error.closed ? { kind: "closed" } : { kind: "waiting", timer: setTimeout(open, 1000) };
    failPending(error);
    handlers.onError(error);
  };

  function open() {
    const source = new EventSource("/events");
    stream = { kind: "connecting", source };
    source.addEventListener("connection", (event) => {
      stream = { kind: "connected", source, connection: event.data };
      handlers.onConnect(send(event.data));
    });
    source.onerror = () => {
      const closed = source.readyState === EventSource.CLOSED;
      drop(
        new Disconnected(closed ? "Lost the connection to ox." : "Lost the connection to ox. Reconnecting.", closed),
      );
    };
    source.onmessage = (event) => {
      // Keep the original token when a Number would change a tool argument on Allow.
      const message = parse(event.data, (_key, value, context?: { source?: string }) => {
        if (typeof value === "number" && context?.source && String(value) !== context.source) {
          return rawJSON(context.source);
        }
        return value;
      });
      if (message.method) return handlers.onNotification(message);
      const call = pending.get(message.id);
      if (!call) return;
      pending.delete(message.id);
      if (message.error) call.reject(new RpcError(message.error.message, message.error.code));
      else call.resolve(message.result);
    };
  }

  // iOS suspends a hidden page's requests, so a reconnect attempt made before
  // the suspension can never answer. Replace it when the page is shown.
  const onVisible = () => {
    if (document.visibilityState !== "visible") return;
    if (stream.kind === "connecting") stream.source.close();
    else if (stream.kind === "waiting") clearTimeout(stream.timer);
    else return;
    open();
  };
  document.addEventListener("visibilitychange", onVisible);
  open();

  // send returns an Rpc bound to one stream, which fails without a request
  // once that stream is no longer the connected one.
  function send(sentOn: string): Rpc {
    const current = () => stream.kind === "connected" && stream.connection === sentOn;
    return (method, params) => {
      if (!current()) return Promise.reject(new Disconnected("Not connected to ox"));
      const id = nextId++;
      return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        const fail = (error: Error) => {
          if (pending.delete(id)) reject(error);
        };
        // A 503 from the relay and a POST that cannot reach it both mean the
        // stream cannot be used. End the stream, which the browser may still
        // hold open, unless it already ended or the page has reconnected; then
        // only this call fails.
        const lost = () => {
          const error = new Disconnected("Lost the connection to ox. Reconnecting.");
          if (current()) drop(error);
          else fail(error);
        };
        fetch(`/rpc?connection=${sentOn}`, {
          method: "POST",
          body: stringify({ jsonrpc: "2.0", id, method, params }),
        })
          .then(async (response) => {
            if (response.ok) return;
            if (response.status === 503) return lost();
            const message = (await response.text()).trim() || `${response.status} ${response.statusText}`.trim();
            fail(new Error(message));
          })
          .catch(lost);
      });
    };
  }

  return {
    rpc(method, params) {
      if (stream.kind !== "connected") return Promise.reject(new Disconnected("Not connected to ox"));
      return send(stream.connection)(method, params);
    },
    close() {
      document.removeEventListener("visibilitychange", onVisible);
      if (stream.kind === "connecting" || stream.kind === "connected") stream.source.close();
      else if (stream.kind === "waiting") clearTimeout(stream.timer);
      stream = { kind: "closed" };
      failPending(new Disconnected("Closed"));
    },
  };
}
