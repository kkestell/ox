import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";

import { browserMessageSchema, type Snapshot } from "./protocol.ts";

function App() {
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [connection, setConnection] = useState("Connecting");

  useEffect(() => {
    const url = new URL("/socket", window.location.href);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(url);
    socket.addEventListener("open", () => {
      socket.send(JSON.stringify({ type: "ping", requestId: "shell-ready" }));
    });
    socket.addEventListener("message", (event) => {
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
        if (parsed.data.connection.status === "unavailable") {
          setConnection("Unavailable");
        }
        return;
      }
      if (parsed.data.requestId === "shell-ready") {
        setConnection(parsed.data.ok ? "Connected" : "Unavailable");
      }
    });
    socket.addEventListener("close", () => setConnection("Disconnected"));
    socket.addEventListener("error", () => setConnection("Unavailable"));
    return () => socket.close();
  }, []);

  return (
    <main>
      <header>
        <h1>Ox</h1>
        <p>Browser ACP client</p>
      </header>
      <section aria-labelledby="connection-heading">
        <h2 id="connection-heading">Connection</h2>
        <p aria-live="polite">{connection}</p>
        {snapshot ? <p>Host revision {snapshot.revision}</p> : null}
      </section>
      {snapshot ? (
        <section aria-labelledby="workspace-heading">
          <h2 id="workspace-heading">Workspace process</h2>
          <p aria-live="polite">{snapshot.workspace.status}</p>
          {snapshot.workspace.diagnostics.length > 0 ? (
            <ul aria-label="Workspace diagnostics">
              {snapshot.workspace.diagnostics.map((diagnostic, index) => (
                <li key={`${index}-${diagnostic}`}>{diagnostic}</li>
              ))}
            </ul>
          ) : null}
        </section>
      ) : null}
    </main>
  );
}

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing application root");
}
createRoot(root).render(<App />);
