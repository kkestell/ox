import { join } from "node:path";

import {
  type BrowserMessage,
  initialSnapshot,
  parseBrowserCommand,
} from "./protocol.ts";

export type HostOptions = {
  assetDirectory?: string;
  hostname?: string;
  port?: number;
};

export type StartedHost = {
  stop(): void;
  url: string;
};

type SocketData = Record<string, never>;

const mimeTypes = new Map([
  [".html", "text/html; charset=utf-8"],
  [".js", "text/javascript; charset=utf-8"],
  [".map", "application/json; charset=utf-8"],
]);

export function startHost(options: HostOptions = {}): StartedHost {
  const assetDirectory = options.assetDirectory ?? join(import.meta.dir, "..", "public");
  const hostname = options.hostname ?? "127.0.0.1";
  const port = options.port ?? 0;
  const sockets = new Set<Bun.ServerWebSocket<SocketData>>();
  const snapshot = initialSnapshot();

  const server = Bun.serve<SocketData>({
    hostname,
    port,
    async fetch(request, server) {
      const url = new URL(request.url);
      if (url.pathname === "/health") {
        return Response.json({ status: "ok" });
      }
      if (url.pathname === "/socket") {
        if (!sameOrigin(request, url)) {
          return new Response("WebSocket origin must match the host", { status: 403 });
        }
        if (server.upgrade(request, { data: {} })) {
          return undefined;
        }
        return new Response("WebSocket upgrade required", { status: 426 });
      }
      if (request.method !== "GET" && request.method !== "HEAD") {
        return new Response("Method not allowed", { status: 405 });
      }
      const pathname = url.pathname === "/" ? "/index.html" : url.pathname;
      if (!pathname.startsWith("/") || pathname.includes("..")) {
        return new Response("Not found", { status: 404 });
      }
      const path = join(assetDirectory, pathname);
      const file = Bun.file(path);
      if (!(await file.exists())) {
        return new Response("Not found", { status: 404 });
      }
      const extension = pathname.slice(pathname.lastIndexOf("."));
      const headers = new Headers({
        "Cache-Control": "no-store",
        "Content-Type": mimeTypes.get(extension) ?? "application/octet-stream",
      });
      return new Response(request.method === "HEAD" ? null : file, { headers });
    },
    websocket: {
      open(socket) {
        sockets.add(socket);
        send(socket, snapshot);
      },
      message(socket, message) {
        if (typeof message !== "string") {
          send(socket, { type: "result", requestId: "unknown", ok: false, error: "invalid browser command" });
          return;
        }
        let value: unknown;
        try {
          value = JSON.parse(message);
        } catch {
          send(socket, { type: "result", requestId: "unknown", ok: false, error: "invalid browser command" });
          return;
        }
        const command = parseBrowserCommand(value);
        if (!command.ok) {
          const requestId = requestID(value);
          send(socket, { type: "result", requestId, ok: false, error: command.error });
          return;
        }
        switch (command.value.type) {
          case "ping":
            send(socket, {
              type: "result",
              requestId: command.value.requestId,
              ok: true,
              value: { revision: snapshot.revision },
            });
        }
      },
      close(socket) {
        sockets.delete(socket);
      },
    },
  });

  return {
    stop() {
      for (const socket of sockets) {
        socket.close();
      }
      server.stop(true);
    },
    url: server.url.toString().replace(/\/$/, ""),
  };
}

function requestID(value: unknown): string {
  if (
    typeof value === "object" &&
    value !== null &&
    "requestId" in value &&
    typeof value.requestId === "string" &&
    value.requestId.length > 0 &&
    value.requestId.length <= 128
  ) {
    return value.requestId;
  }
  return "unknown";
}

function sameOrigin(request: Request, url: URL): boolean {
  const origin = request.headers.get("origin");
  const expected = new URL(url);
  expected.protocol = expected.protocol === "https:" || expected.protocol === "wss:" ? "https:" : "http:";
  return origin === expected.origin;
}

function send(socket: Bun.ServerWebSocket<SocketData>, message: BrowserMessage): void {
  socket.send(JSON.stringify(message));
}

if (import.meta.main) {
  const port = argument("--port");
  const hostname = argument("--host");
  const host = startHost({
    hostname: hostname ?? undefined,
    port: port === undefined ? undefined : Number.parseInt(port, 10),
  });
  process.stdout.write(`Ox browser host listening at ${host.url}\n`);
}

function argument(name: string): string | undefined {
  const index = process.argv.indexOf(name);
  return index === -1 ? undefined : process.argv[index + 1];
}
