import type { Methods, Notification } from "./protocol";

// RpcError is an error response from oxd, with its code.
export class RpcError extends Error {
  code: number;
  constructor(message: string, code: number) {
    super(message);
    this.code = code;
  }
}

export type Connection = {
  rpc<M extends keyof Methods>(method: M, params: Methods[M][0]): Promise<Methods[M][1]>;
  close(): void;
};

// connect opens the page's oxd connection through ox: the event stream
// carries every response and notification, and requests are posted to /rpc.
// onConnect runs each time the stream (re)connects, with a new oxd
// connection that has seen no earlier notifications. onError runs each time
// the stream fails; once ox refuses it, as while oxd is down, the browser
// stops reconnecting.
export function connect(handlers: {
  onConnect(): void;
  onNotification(notification: Notification): void;
  onError(error: Error): void;
}): Connection {
  const source = new EventSource("/events");
  const pending = new Map<number, { resolve(result: unknown): void; reject(error: Error): void }>();
  let connection: string | undefined;
  let nextId = 1;

  const failPending = (error: Error) => {
    for (const call of pending.values()) call.reject(error);
    pending.clear();
  };

  source.addEventListener("connection", (event) => {
    connection = event.data;
    handlers.onConnect();
  });
  source.onerror = () => {
    connection = undefined;
    const error = new Error(
      source.readyState === EventSource.CLOSED
        ? "Lost the connection to ox. Reload the page to reconnect."
        : "Lost the connection to ox. Reconnecting.",
    );
    failPending(error);
    handlers.onError(error);
  };
  source.onmessage = (event) => {
    const message = JSON.parse(event.data);
    if (message.method) return handlers.onNotification(message);
    const call = pending.get(message.id);
    if (!call) return;
    pending.delete(message.id);
    if (message.error) call.reject(new RpcError(message.error.message, message.error.code));
    else call.resolve(message.result);
  };

  return {
    rpc(method, params) {
      if (!connection) return Promise.reject(new Error("Not connected to ox"));
      const id = nextId++;
      return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        const fail = (error: Error) => {
          if (pending.delete(id)) reject(error);
        };
        fetch(`/rpc?connection=${connection}`, {
          method: "POST",
          body: JSON.stringify({ jsonrpc: "2.0", id, method, params }),
        }).then(async (response) => {
          if (!response.ok) fail(new Error(await response.text()));
        }, fail);
      });
    },
    close() {
      source.close();
      failPending(new Error("Closed"));
    },
  };
}
