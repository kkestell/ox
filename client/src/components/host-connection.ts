import { useEffect, useRef, useState } from "react";

import { type BrowserCommand, browserMessageSchema, type Snapshot } from "../protocol.ts";

export type CommandResult = { ok: true } | { error: string; ok: false };

// The host answers a command with a result carrying the request's own
// identifier, so the sender names what to do with its outcome and the
// connection needs to know nothing about the surface that sent it.
export function useHostConnection(): {
  connection: string;
  send: (command: BrowserCommand, onResult?: (result: CommandResult) => void) => boolean;
  snapshot: Snapshot | undefined;
} {
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [connection, setConnection] = useState("Connecting");
  const socket = useRef<WebSocket | undefined>(undefined);
  const pending = useRef(new Map<string, (result: CommandResult) => void>());

  useEffect(() => {
    const url = new URL("/socket", window.location.href);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    const connectionSocket = new WebSocket(url);
    socket.current = connectionSocket;
    connectionSocket.addEventListener("open", () => setConnection("Connected"));
    connectionSocket.addEventListener("message", (event) => {
      if (typeof event.data !== "string") {
        return;
      }
      let message: unknown;
      try {
        message = JSON.parse(event.data);
      } catch {
        return;
      }
      const parsed = browserMessageSchema.safeParse(message);
      if (!parsed.success) {
        return;
      }
      if (parsed.data.type === "snapshot") {
        setSnapshot(parsed.data);
        return;
      }
      const onResult = pending.current.get(parsed.data.requestId);
      pending.current.delete(parsed.data.requestId);
      onResult?.(parsed.data.ok ? { ok: true } : { error: parsed.data.error, ok: false });
    });
    connectionSocket.addEventListener("close", () => setConnection("Disconnected"));
    connectionSocket.addEventListener("error", () => setConnection("Unavailable"));
    return () => {
      socket.current = undefined;
      connectionSocket.close();
    };
  }, []);

  function send(command: BrowserCommand, onResult?: (result: CommandResult) => void): boolean {
    if (socket.current?.readyState !== WebSocket.OPEN) {
      return false;
    }
    socket.current.send(JSON.stringify(command));
    if (onResult) {
      pending.current.set(command.requestId, onResult);
    }
    return true;
  }

  return { connection, send, snapshot };
}
