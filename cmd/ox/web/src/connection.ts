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
// through the relay and its event stream, is gone.
export class Disconnected extends Error {}

export type Connection = {
  rpc<M extends keyof Methods>(method: M, params: Methods[M][0]): Promise<Methods[M][1]>;
  close(): void;
};

export function connect(handlers: {
  onConnect(): void;
  onNotification(notification: Notification): void;
  onError(error: Error): void;
}): Connection {
  const pending = new Map<number, { resolve(result: unknown): void; reject(error: Error): void }>();
  let source: EventSource;
  let connection: string | undefined;
  let nextId = 1;

  const failPending = (error: Error) => {
    for (const call of pending.values()) call.reject(error);
    pending.clear();
  };

  const lose = (error: Disconnected) => {
    connection = undefined;
    failPending(error);
    handlers.onError(error);
  };

  const open = () => {
    source = new EventSource("/events");
    source.addEventListener("connection", (event) => {
      connection = event.data;
      handlers.onConnect();
    });
    source.onerror = () => {
      lose(
        new Disconnected(
          source.readyState === EventSource.CLOSED
            ? "Lost the connection to ox. Reload the page to reconnect."
            : "Lost the connection to ox. Reconnecting.",
        ),
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
  };
  open();

  return {
    rpc(method, params) {
      if (!connection) return Promise.reject(new Disconnected("Not connected to ox"));
      const id = nextId++;
      const sentOn = connection;
      return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        const fail = (error: Error) => {
          if (pending.delete(id)) reject(error);
        };
        fetch(`/rpc?connection=${sentOn}`, {
          method: "POST",
          body: stringify({ jsonrpc: "2.0", id, method, params }),
        })
          .then(async (response) => {
            if (response.ok) return;
            const message = (await response.text()).trim() || `${response.status} ${response.statusText}`.trim();
            // The relay answers 503 when the page's oxd connection is gone.
            fail(response.status === 503 ? new Disconnected(message) : new Error(message));
          })
          .catch(() => {
            // A POST that cannot reach the relay means the stream cannot either.
            // Replace the stream, which the browser may still hold open, unless
            // the stream already reported the drop or the page has reconnected.
            if (connection !== sentOn) return fail(new Disconnected("Lost the connection to ox. Reconnecting."));
            source.close();
            lose(new Disconnected("Lost the connection to ox. Reconnecting."));
            open();
          });
      });
    },
    close() {
      source.close();
      connection = undefined;
      failPending(new Disconnected("Closed"));
    },
  };
}
