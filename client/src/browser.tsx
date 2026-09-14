import { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";

import { type BrowserCommand, browserMessageSchema, type Snapshot } from "./protocol.ts";

function App() {
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [connection, setConnection] = useState("Connecting");
  const socket = useRef<WebSocket | undefined>(undefined);
  const [credential, setCredential] = useState("");

  useEffect(() => {
    const url = new URL("/socket", window.location.href);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    const connectionSocket = new WebSocket(url);
    socket.current = connectionSocket;
    connectionSocket.addEventListener("open", () => {
      connectionSocket.send(JSON.stringify({ type: "ping", requestId: "shell-ready" }));
    });
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
        if (parsed.data.connection.status === "unavailable") {
          setConnection("Unavailable");
        }
        return;
      }
      if (parsed.data.requestId === "shell-ready") {
        setConnection(parsed.data.ok ? "Connected" : "Unavailable");
      }
    });
    connectionSocket.addEventListener("close", () => setConnection("Disconnected"));
    connectionSocket.addEventListener("error", () => setConnection("Unavailable"));
    return () => {
      socket.current = undefined;
      connectionSocket.close();
    };
  }, []);

  function send(command: BrowserCommand): void {
    if (socket.current?.readyState !== WebSocket.OPEN) {
      return;
    }
    socket.current.send(JSON.stringify(command));
  }

  function terminalLogin(methodId: string): void {
    const value = credential;
    setCredential("");
    send({ credential: value, methodId, requestId: crypto.randomUUID(), type: "login" });
  }

  function catalogCommand(type: "new-session" | "next-session-page" | "refresh-sessions"): void {
    send({ requestId: crypto.randomUUID(), type });
  }

  function sessionCommand(
    type: "close-session" | "delete-session" | "load-session" | "resume-session",
    sessionId: string,
  ): void {
    send({ requestId: crypto.randomUUID(), sessionId, type });
  }

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
      {snapshot ? (
        <section aria-labelledby="authentication-heading">
          <h2 id="authentication-heading">Authentication</h2>
          <p aria-live="polite">{snapshot.authentication.status}</p>
          {snapshot.authentication.error ? <p role="alert">{snapshot.authentication.error}</p> : null}
          {snapshot.authentication.methods.map((method) =>
            method.type === "agent" ? (
              <button
                key={method.id}
                disabled={snapshot.authentication.status === "working"}
                onClick={() => send({ methodId: method.id, requestId: crypto.randomUUID(), type: "authenticate" })}
                type="button"
              >
                {method.name}
              </button>
            ) : (
              <form
                key={method.id}
                onSubmit={(event) => {
                  event.preventDefault();
                  terminalLogin(method.id);
                }}
              >
                <label>
                  {`${method.name} credential`}
                  <input
                    autoComplete="off"
                    disabled={snapshot.authentication.status === "working"}
                    onChange={(event) => setCredential(event.target.value)}
                    required
                    type="password"
                    value={credential}
                  />
                </label>
                <button disabled={snapshot.authentication.status === "working"} type="submit">
                  {method.name}
                </button>
              </form>
            ),
          )}
          {snapshot.authentication.logoutAvailable ? (
            <button
              disabled={snapshot.authentication.status === "working"}
              onClick={() => send({ requestId: crypto.randomUUID(), type: "logout" })}
              type="button"
            >
              Log out
            </button>
          ) : null}
        </section>
      ) : null}
      {snapshot ? (
        <section aria-labelledby="sessions-heading">
          <h2 id="sessions-heading">Sessions</h2>
          <p aria-live="polite">
            {snapshot.sessions.selectedId ? `Selected session ${snapshot.sessions.selectedId}` : "No session selected"}
          </p>
          <button onClick={() => catalogCommand("new-session")} type="button">
            New session
          </button>
          <button onClick={() => catalogCommand("refresh-sessions")} type="button">
            Refresh sessions
          </button>
          <ul aria-label="Sessions">
            {snapshot.sessions.values.map((value) => (
              <li key={value.id}>
                <p>{value.title ?? value.id}</p>
                <p>{value.status}</p>
                {value.updatedAt ? <p>{value.updatedAt}</p> : null}
                {value.status === "inactive" ? (
                  <>
                    <button onClick={() => sessionCommand("load-session", value.id)} type="button">
                      Load
                    </button>
                    <button onClick={() => sessionCommand("resume-session", value.id)} type="button">
                      Resume
                    </button>
                    <button onClick={() => sessionCommand("delete-session", value.id)} type="button">
                      Delete
                    </button>
                  </>
                ) : (
                  <button onClick={() => sessionCommand("close-session", value.id)} type="button">
                    Close
                  </button>
                )}
              </li>
            ))}
          </ul>
          {snapshot.sessions.nextCursor ? (
            <button onClick={() => catalogCommand("next-session-page")} type="button">
              More sessions
            </button>
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
