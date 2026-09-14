import { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";

import {
  type BrowserCommand,
  browserMessageSchema,
  type SessionTranscript,
  type Snapshot,
  type ToolTranscriptContent,
  type TranscriptContent,
} from "./protocol.ts";

function App() {
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [connection, setConnection] = useState("Connecting");
  const socket = useRef<WebSocket | undefined>(undefined);
  const [credential, setCredential] = useState("");
  const [sessionError, setSessionError] = useState<string>();
  const sessionRequests = useRef(new Set<string>());

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
        return;
      }
      if (sessionRequests.current.delete(parsed.data.requestId)) {
        setSessionError(parsed.data.ok ? undefined : parsed.data.error);
      }
    });
    connectionSocket.addEventListener("close", () => setConnection("Disconnected"));
    connectionSocket.addEventListener("error", () => setConnection("Unavailable"));
    return () => {
      socket.current = undefined;
      connectionSocket.close();
    };
  }, []);

  function send(command: BrowserCommand): boolean {
    if (socket.current?.readyState !== WebSocket.OPEN) {
      return false;
    }
    socket.current.send(JSON.stringify(command));
    return true;
  }

  // Session commands report their outcome only through their result, so the
  // browser keeps the latest failure visible until another one succeeds.
  function submitSessionCommand(command: BrowserCommand): void {
    if (send(command)) {
      sessionRequests.current.add(command.requestId);
      return;
    }
    setSessionError("The host connection is not open");
  }

  function terminalLogin(methodId: string): void {
    const value = credential;
    setCredential("");
    send({ credential: value, methodId, requestId: crypto.randomUUID(), type: "login" });
  }

  function catalogCommand(type: "new-session" | "next-session-page" | "refresh-sessions"): void {
    submitSessionCommand({ requestId: crypto.randomUUID(), type });
  }

  function sessionCommand(
    type: "close-session" | "delete-session" | "load-session" | "resume-session",
    sessionId: string,
  ): void {
    submitSessionCommand({ requestId: crypto.randomUUID(), sessionId, type });
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
          {sessionError ? <p role="alert">{sessionError}</p> : null}
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
            {snapshot.sessions.values.map((value) => {
              // Every session repeats the same controls, so each one names its session.
              const label = value.title ?? value.id;
              return (
                <li key={value.id}>
                  <p>{label}</p>
                  <p>{value.status}</p>
                  {value.updatedAt ? <p>{value.updatedAt}</p> : null}
                  {value.status === "inactive" ? (
                    <>
                      <button
                        aria-label={`Load ${label}`}
                        onClick={() => sessionCommand("load-session", value.id)}
                        type="button"
                      >
                        Load
                      </button>
                      <button
                        aria-label={`Resume ${label}`}
                        onClick={() => sessionCommand("resume-session", value.id)}
                        type="button"
                      >
                        Resume
                      </button>
                      <button
                        aria-label={`Delete ${label}`}
                        onClick={() => sessionCommand("delete-session", value.id)}
                        type="button"
                      >
                        Delete
                      </button>
                    </>
                  ) : (
                    <button
                      aria-label={`Close ${label}`}
                      onClick={() => sessionCommand("close-session", value.id)}
                      type="button"
                    >
                      Close
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
          {snapshot.sessions.nextCursor ? (
            <button onClick={() => catalogCommand("next-session-page")} type="button">
              More sessions
            </button>
          ) : null}
        </section>
      ) : null}
      {snapshot?.sessions.active ? <Transcript transcript={snapshot.sessions.active.transcript} /> : null}
    </main>
  );
}

function Transcript({ transcript }: { transcript: SessionTranscript }) {
  return (
    <section aria-labelledby="transcript-heading">
      <h2 id="transcript-heading">Transcript</h2>
      <ol aria-label="Session transcript">
        {transcript.entries.map((entry) => (
          <li key={entry.id}>
            {entry.kind === "tool" ? (
              <article aria-label={`Tool ${entry.title}`}>
                <h3>{entry.title}</h3>
                {entry.name ? <p>{entry.name}</p> : null}
                {entry.toolKind ? <p>{entry.toolKind}</p> : null}
                {entry.status ? <p>{entry.status}</p> : null}
                {entry.locations.length > 0 ? (
                  <ul aria-label="Tool locations">
                    {entry.locations.map((location) => (
                      <li key={`${location.path}:${location.line ?? ""}`}>
                        {location.path}
                        {location.line === undefined ? "" : `:${location.line}`}
                      </li>
                    ))}
                  </ul>
                ) : null}
                {entry.content.map((content, index) => (
                  <ToolOutput content={content} key={index} />
                ))}
              </article>
            ) : entry.kind === "unknown" ? (
              <p>{entry.label}</p>
            ) : (
              <article aria-label={`${entry.kind} message`}>
                <h3>{entry.kind === "thought" ? "Thought" : entry.kind === "agent" ? "Agent" : "User"}</h3>
                {entry.content.map((content, index) => (
                  <Content content={content} key={index} />
                ))}
              </article>
            )}
          </li>
        ))}
      </ol>
      <section aria-labelledby="plan-heading">
        <h3 id="plan-heading">Plan</h3>
        <ol>
          {transcript.plan.map((entry, index) => (
            <li key={index}>
              {entry.content} ({entry.status}, {entry.priority})
            </li>
          ))}
        </ol>
      </section>
      {transcript.usage ? (
        <section aria-labelledby="usage-heading">
          <h3 id="usage-heading">Usage</h3>
          <p>{`${transcript.usage.used} of ${transcript.usage.size} context tokens`}</p>
          {transcript.usage.cost ? <p>{`${transcript.usage.cost.amount} ${transcript.usage.cost.currency}`}</p> : null}
        </section>
      ) : null}
      <section aria-labelledby="configuration-heading">
        <h3 id="configuration-heading">Configuration</h3>
        <dl>
          {transcript.configuration.map((option) => (
            <div key={option.id}>
              <dt>{option.name}</dt>
              <dd>{String(option.currentValue)}</dd>
              {option.description ? <dd>{option.description}</dd> : null}
            </div>
          ))}
        </dl>
      </section>
    </section>
  );
}

function ToolOutput({ content }: { content: ToolTranscriptContent }) {
  switch (content.type) {
    case "content":
      return <Content content={content.content} />;
    case "diff":
      return <pre>{`${content.path}\n${content.oldText ?? ""}\n${content.newText}`}</pre>;
    case "terminal":
      return <p>{`Terminal ${content.terminalId}`}</p>;
    case "unknown":
      return <p>{content.label}</p>;
  }
}

function Content({ content }: { content: TranscriptContent }) {
  switch (content.type) {
    case "text":
      return <p>{content.text}</p>;
    case "image":
      return <img alt={content.uri ?? "Image content"} src={`data:${content.mimeType};base64,${content.data}`} />;
    case "audio":
      return <audio controls src={`data:${content.mimeType};base64,${content.data}`} />;
    case "resource_link": {
      const href = safeResourceLink(content.uri);
      const label = content.title ?? content.name;
      return (
        <p>
          {href ? <a href={href}>{label}</a> : <span>{label}</span>}
          {content.description ? `: ${content.description}` : ""}
        </p>
      );
    }
    case "resource":
      return content.text === undefined ? (
        <a download href={`data:${content.mimeType ?? "application/octet-stream"};base64,${content.blob}`}>
          {content.uri}
        </a>
      ) : (
        <pre>{content.text}</pre>
      );
    case "unknown":
      return <p>{content.label}</p>;
  }
}

// ACP resource links are agent-provided values, so do not let them select an
// executable browser URL scheme.
function safeResourceLink(uri: string): string | undefined {
  try {
    const url = new URL(uri);
    return url.protocol === "http:" || url.protocol === "https:" ? url.href : undefined;
  } catch {
    return undefined;
  }
}

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing application root");
}
createRoot(root).render(<App />);
