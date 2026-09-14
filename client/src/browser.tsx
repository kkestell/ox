import { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";

import {
  type BrowserCommand,
  browserMessageSchema,
  maximumAttachmentBytes,
  maximumPromptText,
  type MCPServer,
  type PromptCapabilities,
  type PromptContentBlock,
  type PendingInteraction,
  type FormValue,
  type SessionTranscript,
  type Snapshot,
  type ToolTranscriptContent,
  type TranscriptContent,
} from "./protocol.ts";

// A supervisor-routed command names the workspace it acts on, so the browser
// stamps the current selection once instead of at every call site.
type RoutedCommand = Exclude<
  Extract<BrowserCommand, { workspaceId: string }>,
  { type: "remove-workspace" | "restart-workspace" | "select-workspace" }
>;
type RoutedRequest = RoutedCommand extends infer Command
  ? Command extends RoutedCommand
    ? Omit<Command, "workspaceId">
    : never
  : never;

function App() {
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [connection, setConnection] = useState("Connecting");
  const socket = useRef<WebSocket | undefined>(undefined);
  const [credential, setCredential] = useState("");
  const [mcpServers, setMCPServers] = useState<MCPServer[]>([]);
  const [mcpMessage, setMCPMessage] = useState<string>();
  const [sessionError, setSessionError] = useState<string>();
  const [workspacePath, setWorkspacePath] = useState("");
  const [settings, setSettings] = useState(false);
  const [workspaceMessage, setWorkspaceMessage] = useState<{ error: boolean; text: string }>();
  const sessionRequests = useRef(new Set<string>());
  const mcpRequests = useRef(new Set<string>());
  const workspaceRequests = useRef(new Map<string, BrowserCommand["type"]>());
  const active = snapshot?.sessions.active;

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
      const workspaceRequest = workspaceRequests.current.get(parsed.data.requestId);
      if (workspaceRequest !== undefined) {
        workspaceRequests.current.delete(parsed.data.requestId);
        if (parsed.data.ok) {
          if (workspaceRequest === "register-workspace") {
            setWorkspacePath("");
          }
          setWorkspaceMessage({
            error: false,
            text: workspaceRequest === "restart-workspace" ? "Ox restarted" : "Workspace registry updated",
          });
        } else {
          setWorkspaceMessage({ error: true, text: parsed.data.error });
        }
      }
      if (sessionRequests.current.delete(parsed.data.requestId)) {
        setSessionError(parsed.data.ok ? undefined : parsed.data.error);
      }
      if (mcpRequests.current.delete(parsed.data.requestId)) {
        if (parsed.data.ok) {
          setMCPServers([]);
          setMCPMessage("MCP servers saved");
        } else {
          setMCPMessage(parsed.data.error);
        }
      }
    });
    connectionSocket.addEventListener("close", () => setConnection("Disconnected"));
    connectionSocket.addEventListener("error", () => setConnection("Unavailable"));
    return () => {
      socket.current = undefined;
      connectionSocket.close();
    };
  }, []);

  const selectedWorkspaceId = snapshot?.workspaces.selectedId;
  // Settings act on whichever workspace is selected, and another browser can
  // change that selection, so a draft written for one workspace never follows
  // the browser to the next.
  useEffect(() => {
    setCredential("");
    setMCPServers([]);
    setMCPMessage(undefined);
    setSessionError(undefined);
  }, [selectedWorkspaceId]);

  function send(command: BrowserCommand): boolean {
    if (socket.current?.readyState !== WebSocket.OPEN) {
      return false;
    }
    socket.current.send(JSON.stringify(command));
    return true;
  }

  // Returns why the command could not be sent, so a missing selection is not
  // reported as a lost host connection. The sidebar acts on a named workspace,
  // which is how opening or creating a conversation elsewhere stays one command.
  function sendRouted(request: RoutedRequest, named?: string): string | undefined {
    const workspaceId = named ?? snapshot?.workspaces.selectedId;
    if (workspaceId === undefined) {
      return "No workspace is selected";
    }
    return send({ ...request, workspaceId } as RoutedCommand) ? undefined : "The host connection is not open";
  }

  // Session commands report their outcome only through their result, so the
  // browser keeps the latest failure visible until another one succeeds.
  function submitSessionCommand(request: RoutedRequest, named?: string): void {
    const problem = sendRouted(request, named);
    if (problem !== undefined) {
      setSessionError(problem);
      return;
    }
    sessionRequests.current.add(request.requestId);
  }

  function terminalLogin(methodId: string): void {
    const value = credential;
    setCredential("");
    sendRouted({ credential: value, methodId, requestId: crypto.randomUUID(), type: "login" });
  }

  function historyCommand(type: "next-history-page" | "refresh-history"): void {
    submitSessionCommand({ requestId: crypto.randomUUID(), type });
  }

  function conversationCommand(
    type: "close-conversation" | "delete-conversation" | "open-conversation",
    sessionId: string,
  ): void {
    submitSessionCommand({ requestId: crypto.randomUUID(), sessionId, type });
  }

  function saveMCPServers(servers: MCPServer[]): void {
    const requestId = crypto.randomUUID();
    const problem = sendRouted({ mcpServers: servers, requestId, type: "set-mcp-servers" });
    if (problem !== undefined) {
      setMCPMessage(problem);
      return;
    }
    mcpRequests.current.add(requestId);
    setMCPMessage("Saving MCP servers…");
  }

  function submitWorkspaceCommand(command: BrowserCommand): void {
    if (!send(command)) {
      setWorkspaceMessage({ error: true, text: "The host connection is not open" });
      return;
    }
    workspaceRequests.current.set(command.requestId, command.type);
    setWorkspaceMessage(undefined);
  }

  // The catalog entry survives a workspace losing its process, so process
  // problems and their recovery read from it rather than from the detail the
  // host only publishes while a supervisor exists.
  const selectedWorkspace = snapshot?.workspaces.values.find((entry) => entry.id === snapshot.workspaces.selectedId);
  const conversations = selectedWorkspace?.conversations ?? [];
  const selected = conversations.find((conversation) => conversation.id === snapshot?.sessions.selectedId);
  const title = selected?.title ?? "New conversation";
  const authenticated = snapshot?.authentication.status === "authenticated";
  const unavailable =
    connection === "Unavailable" || connection === "Disconnected" || selectedWorkspace?.status === "unavailable";
  function restartWorkspace(workspaceId: string): void {
    setSessionError(undefined);
    submitWorkspaceCommand({ requestId: crypto.randomUUID(), type: "restart-workspace", workspaceId });
  }

  // Settings are published for the selected workspace only, so reaching another
  // workspace's settings selects it. Everything the surface renders comes from
  // the selection, so a refused change shows the workspace it is still showing
  // rather than the one that was clicked.
  function showSettings(workspaceId: string): void {
    setSettings(true);
    if (workspaceId === selectedWorkspaceId) {
      return;
    }
    if (!send({ requestId: crypto.randomUUID(), type: "select-workspace", workspaceId })) {
      setWorkspaceMessage({ error: true, text: "The host connection is not open" });
    }
  }

  return (
    <div className="layout">
      {snapshot ? (
        <Sidebar
          message={workspaceMessage}
          onNew={(workspaceId) => {
            setSettings(false);
            submitSessionCommand({ requestId: crypto.randomUUID(), type: "new-conversation" }, workspaceId);
          }}
          onOlder={() => historyCommand("next-history-page")}
          onOpen={(workspaceId, sessionId) => {
            setSettings(false);
            submitSessionCommand({ requestId: crypto.randomUUID(), sessionId, type: "open-conversation" }, workspaceId);
          }}
          onPath={setWorkspacePath}
          onRegister={() =>
            submitWorkspaceCommand({ path: workspacePath, requestId: crypto.randomUUID(), type: "register-workspace" })
          }
          onRemove={(workspaceId) =>
            submitWorkspaceCommand({ requestId: crypto.randomUUID(), type: "remove-workspace", workspaceId })
          }
          onRestart={restartWorkspace}
          onSettings={showSettings}
          path={workspacePath}
          sessions={snapshot.sessions}
          settings={settings}
          workspaces={snapshot.workspaces}
        />
      ) : null}
      <main>
        {unavailable ? <p role="alert">Ox is unavailable. Open workspace settings for diagnostics.</p> : null}
        {sessionError ? <p role="alert">{sessionError}</p> : null}
        {snapshot?.workspace && settings ? (
          <section aria-label="Workspace settings">
            <h2>{snapshot.workspace.name}</h2>
            <Authentication
              authentication={snapshot.authentication}
              credential={credential}
              onAuthenticate={(methodId) => sendRouted({ methodId, requestId: crypto.randomUUID(), type: "authenticate" })}
              onCredential={setCredential}
              onLogin={terminalLogin}
              onLogout={() => sendRouted({ requestId: crypto.randomUUID(), type: "logout" })}
            />
            <section aria-labelledby="mcp-heading">
              <h3 id="mcp-heading">MCP servers</h3>
              <p>{`${snapshot.workspace.mcpServerCount} configured`}</p>
              {mcpMessage ? <p aria-live="polite">{mcpMessage}</p> : null}
              <MCPActivationForm onSave={saveMCPServers} servers={mcpServers} setServers={setMCPServers} />
            </section>
            <SupportDetails
              activeSessionId={active?.id}
              connection={connection}
              onRefresh={() => historyCommand("refresh-history")}
              snapshot={snapshot}
              workspace={snapshot.workspace}
            />
          </section>
        ) : snapshot?.workspace && !authenticated && !unavailable ? (
          <p>
            {"Ox is not connected. Connect it in "}
            <a href={`#settings-${selectedWorkspaceId}`} onClick={() => setSettings(true)}>workspace settings</a>.
          </p>
        ) : snapshot?.workspace && active ? (
          <section aria-label="Conversation">
            <header>
              <h2>{title}</h2>
              <p>{selectedWorkspace?.name}</p>
              <SessionInformation
                onConfigOption={(configId, value) =>
                  submitSessionCommand({ configId, requestId: crypto.randomUUID(), sessionId: active.id, type: "set-config-option", value })
                }
                transcript={active.transcript}
              />
              <details>
                <summary>Conversation actions</summary>
                <button onClick={() => conversationCommand("close-conversation", active.id)} type="button">Close conversation</button>
                <button onClick={() => conversationCommand("delete-conversation", active.id)} type="button">Delete conversation</button>
              </details>
            </header>
            <Transcript
              interactions={active.interactions}
              onElicitation={(interactionId, action, content) =>
                submitSessionCommand({ action, ...(content === undefined ? {} : { content }), interactionId, requestId: crypto.randomUUID(), sessionId: active.id, type: "resolve-elicitation" })
              }
              onPermission={(interactionId, optionId) =>
                submitSessionCommand({ interactionId, optionId, requestId: crypto.randomUUID(), sessionId: active.id, type: "resolve-permission" })
              }
              transcript={active.transcript}
            />
            <Composer
              busy={active.busy}
              capabilities={snapshot.workspace.promptCapabilities}
              key={active.id}
              onCancel={() => submitSessionCommand({ requestId: crypto.randomUUID(), sessionId: active.id, type: "cancel-prompt" })}
              onError={setSessionError}
              onSubmit={(prompt) => submitSessionCommand({ prompt, requestId: crypto.randomUUID(), sessionId: active.id, type: "prompt" })}
            />
          </section>
        ) : snapshot?.workspace ? (
          <p>Choose a conversation or start a new one.</p>
        ) : null}
      </main>
    </div>
  );
}

// Navigating between conversations is a link, so the sidebar reads as
// navigation; every control that changes host state stays a button.
function Sidebar({ message, onNew, onOlder, onOpen, onPath, onRegister, onRemove, onRestart, onSettings, path, sessions, settings, workspaces }: {
  message?: { error: boolean; text: string };
  onNew: (workspaceId: string) => void;
  onOlder: () => void;
  onOpen: (workspaceId: string, sessionId: string) => void;
  onPath: (path: string) => void;
  onRegister: () => void;
  onRemove: (workspaceId: string) => void;
  onRestart: (workspaceId: string) => void;
  onSettings: (workspaceId: string) => void;
  path: string;
  sessions: Snapshot["sessions"];
  settings: boolean;
  workspaces: Snapshot["workspaces"];
}) {
  return (
    <nav aria-label="Workspaces and conversations">
      <h1>Ox</h1>
      {workspaces.values.length > 0 ? (
        <ul aria-label="Workspaces">
          {workspaces.values.map((workspace) => (
            <li aria-current={workspaces.selectedId === workspace.id ? "true" : undefined} key={workspace.id}>
              <h2>{workspace.name}</h2>
              <button aria-label={`New conversation in ${workspace.name}`} onClick={() => onNew(workspace.id)} type="button">+</button>
              <a
                aria-current={settings && workspaces.selectedId === workspace.id ? "page" : undefined}
                aria-label={`Settings for ${workspace.name}`}
                href={`#settings-${workspace.id}`}
                onClick={() => onSettings(workspace.id)}
              >
                Settings
              </a>
              <button aria-label={`Remove ${workspace.name}`} onClick={() => onRemove(workspace.id)} type="button">Remove</button>
              {workspace.status === "ready" ? (
                <>
                  <ul aria-label={`${workspace.name} conversations`}>
                    {workspace.conversations.map((conversation) => (
                      <li key={conversation.id}>
                        <a
                          aria-current={!settings && sessions.selectedId === conversation.id ? "page" : undefined}
                          href={`#${conversation.id}`}
                          onClick={() => onOpen(workspace.id, conversation.id)}
                        >
                          {conversation.title ?? "Untitled conversation"}
                        </a>
                        {conversation.status === "loading" ? <span>Opening…</span> : null}
                        {conversation.awaiting ? <span>Waiting for you</span> : null}
                        {conversation.updatedAt ? <time dateTime={conversation.updatedAt}>{new Date(conversation.updatedAt).toLocaleString()}</time> : null}
                      </li>
                    ))}
                  </ul>
                  {workspaces.selectedId === workspace.id && sessions.nextCursor ? (
                    <button onClick={onOlder} type="button">Show older conversations</button>
                  ) : null}
                </>
              ) : (
                <p>
                  {workspace.status === "starting" ? "Ox is starting." : "Ox is not running."}
                  {workspace.status === "starting" ? null : (
                    <button aria-label={`Restart ${workspace.name}`} onClick={() => onRestart(workspace.id)} type="button">Restart</button>
                  )}
                </p>
              )}
            </li>
          ))}
        </ul>
      ) : <p>Register a server-local workspace to begin.</p>}
      <form onSubmit={(event) => { event.preventDefault(); onRegister(); }}>
        <label>
          Workspace path
          <input onChange={(event) => onPath(event.target.value)} required value={path} />
        </label>
        <button type="submit">Register workspace</button>
      </form>
      {message ? <p aria-live="polite" role={message.error ? "alert" : undefined}>{message.text}</p> : null}
    </nav>
  );
}

// Connecting and managing a credential are the same surface: the methods Ox
// advertises name their own controls, and a stored credential is worth
// attempting only while the workspace is unauthenticated.
function Authentication({ authentication, credential, onAuthenticate, onCredential, onLogin, onLogout }: {
  authentication: Snapshot["authentication"];
  credential: string;
  onAuthenticate: (methodId: string) => void;
  onCredential: (credential: string) => void;
  onLogin: (methodId: string) => void;
  onLogout: () => void;
}) {
  const working = authentication.status === "working";
  const authenticated = authentication.status === "authenticated";
  return (
    <section aria-labelledby="authentication-heading">
      <h3 id="authentication-heading">Authentication</h3>
      {authentication.error ? <p role="alert">{authentication.error}</p> : null}
      <p>{authenticated ? "Connected" : "Not connected"}</p>
      {authentication.methods.map((method) => method.type === "agent" ? (
        authenticated ? null : (
          <button disabled={working} key={method.id} onClick={() => onAuthenticate(method.id)} type="button">Use configured credential</button>
        )
      ) : (
        <form key={method.id} onSubmit={(event) => { event.preventDefault(); onLogin(method.id); }}>
          <label>{`${method.name} credential`}<input autoComplete="off" disabled={working} onChange={(event) => onCredential(event.target.value)} required type="password" value={credential} /></label>
          <button disabled={working} type="submit">{method.name}</button>
        </form>
      ))}
      {authentication.logoutAvailable ? <button disabled={working} onClick={onLogout} type="button">Log out</button> : null}
    </section>
  );
}

function SupportDetails({ activeSessionId, connection, onRefresh, snapshot, workspace }: {
  activeSessionId?: string;
  connection: string;
  onRefresh: () => void;
  snapshot: Snapshot;
  workspace: NonNullable<Snapshot["workspace"]>;
}) {
  return (
    <section aria-labelledby="support-heading">
      <h3 id="support-heading">Support details</h3>
      <dl>
        <dt>Browser connection</dt><dd>{connection}</dd>
        <dt>Host revision</dt><dd>{snapshot.revision}</dd>
        {activeSessionId ? <><dt>Session ID</dt><dd>{activeSessionId}</dd></> : null}
      </dl>
      <ul aria-label="Ox processes">
        {snapshot.workspaces.values.map((entry) => (
          <li key={entry.id}>
            {`${entry.name} — ${entry.status}, ${entry.busy ? "working" : "idle"}${entry.awaiting ? ", waiting for an answer" : ""}`}
          </li>
        ))}
      </ul>
      {workspace.diagnostics.length > 0 ? <ul aria-label="Workspace diagnostics">{workspace.diagnostics.map((diagnostic, index) => <li key={`${index}-${diagnostic}`}>{diagnostic}</li>)}</ul> : null}
      <button onClick={onRefresh} type="button">Refresh conversation history</button>
    </section>
  );
}

function MCPActivationForm({
  onSave,
  servers,
  setServers,
}: {
  onSave: (servers: MCPServer[]) => void;
  servers: MCPServer[];
  setServers: (servers: MCPServer[] | ((current: MCPServer[]) => MCPServer[])) => void;
}) {
  function replace(index: number, server: MCPServer): void {
    setServers((current) => current.map((value, currentIndex) => (currentIndex === index ? server : value)));
  }

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        onSave(servers);
      }}
    >
      <fieldset>
        <legend>MCP servers</legend>
        <p>Saved definitions are used for later conversation activations. Secret values are never returned to the browser.</p>
        {servers.map((server, index) => (
          <fieldset key={index}>
            <legend>{server.transport === "http" ? "HTTP MCP server" : "Stdio MCP server"}</legend>
            <button onClick={() => setServers((current) => current.filter((_, currentIndex) => currentIndex !== index))} type="button">
              Remove server
            </button>
            <label>
              Name
              <input
                maxLength={512}
                onChange={(event) => replace(index, { ...server, name: event.target.value })}
                required
                value={server.name}
              />
            </label>
            {server.transport === "http" ? (
              <>
                <label>
                  URL
                  <input
                    maxLength={4096}
                    onChange={(event) => replace(index, { ...server, url: event.target.value })}
                    required
                    type="url"
                    value={server.url}
                  />
                </label>
                <fieldset>
                  <legend>HTTP headers</legend>
                  {server.headers.map((header, headerIndex) => (
                    <div key={headerIndex}>
                      <label>
                        Header name
                        <input
                          maxLength={256}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              headers: server.headers.map((value, currentIndex) =>
                                currentIndex === headerIndex ? { ...value, name: event.target.value } : value,
                              ),
                            })
                          }
                          required
                          value={header.name}
                        />
                      </label>
                      <label>
                        Header value
                        <input
                          autoComplete="off"
                          maxLength={16_384}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              headers: server.headers.map((value, currentIndex) =>
                                currentIndex === headerIndex ? { ...value, value: event.target.value } : value,
                              ),
                            })
                          }
                          type="password"
                          value={header.value}
                        />
                      </label>
                      <button
                        aria-label={`Remove header ${headerIndex + 1}`}
                        onClick={() => replace(index, { ...server, headers: server.headers.filter((_, currentIndex) => currentIndex !== headerIndex) })}
                        type="button"
                      >
                        Remove header
                      </button>
                    </div>
                  ))}
                  <button onClick={() => replace(index, { ...server, headers: [...server.headers, { name: "", value: "" }] })} type="button">
                    Add header
                  </button>
                </fieldset>
              </>
            ) : (
              <>
                <label>
                  Command
                  <input
                    maxLength={4096}
                    onChange={(event) => replace(index, { ...server, command: event.target.value })}
                    required
                    value={server.command}
                  />
                </label>
                <fieldset>
                  <legend>Arguments</legend>
                  {server.args.map((argument, argumentIndex) => (
                    <div key={argumentIndex}>
                      <label>
                        Argument {argumentIndex + 1}
                        <input
                          maxLength={16_384}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              args: server.args.map((value, currentIndex) => (currentIndex === argumentIndex ? event.target.value : value)),
                            })
                          }
                          value={argument}
                        />
                      </label>
                      <button
                        aria-label={`Remove argument ${argumentIndex + 1}`}
                        onClick={() => replace(index, { ...server, args: server.args.filter((_, currentIndex) => currentIndex !== argumentIndex) })}
                        type="button"
                      >
                        Remove argument
                      </button>
                    </div>
                  ))}
                  <button onClick={() => replace(index, { ...server, args: [...server.args, ""] })} type="button">
                    Add argument
                  </button>
                </fieldset>
                <fieldset>
                  <legend>Environment</legend>
                  {server.env.map((variable, variableIndex) => (
                    <div key={variableIndex}>
                      <label>
                        Variable name
                        <input
                          maxLength={256}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              env: server.env.map((value, currentIndex) =>
                                currentIndex === variableIndex ? { ...value, name: event.target.value } : value,
                              ),
                            })
                          }
                          required
                          value={variable.name}
                        />
                      </label>
                      <label>
                        Variable value
                        <input
                          autoComplete="off"
                          maxLength={16_384}
                          onChange={(event) =>
                            replace(index, {
                              ...server,
                              env: server.env.map((value, currentIndex) =>
                                currentIndex === variableIndex ? { ...value, value: event.target.value } : value,
                              ),
                            })
                          }
                          type="password"
                          value={variable.value}
                        />
                      </label>
                      <button
                        aria-label={`Remove variable ${variableIndex + 1}`}
                        onClick={() => replace(index, { ...server, env: server.env.filter((_, currentIndex) => currentIndex !== variableIndex) })}
                        type="button"
                      >
                        Remove variable
                      </button>
                    </div>
                  ))}
                  <button onClick={() => replace(index, { ...server, env: [...server.env, { name: "", value: "" }] })} type="button">
                    Add variable
                  </button>
                </fieldset>
              </>
            )}
          </fieldset>
        ))}
        <button onClick={() => setServers((current) => [...current, { transport: "http", name: "", url: "", headers: [] }])} type="button">
          Add HTTP MCP server
        </button>
        <button onClick={() => setServers((current) => [...current, { transport: "stdio", name: "", command: "", args: [], env: [] }])} type="button">
          Add stdio MCP server
        </button>
        <button type="submit">Save MCP servers</button>
      </fieldset>
    </form>
  );
}

// The composer holds the draft for one session. Mounting it under the session
// key keeps a draft from following the selection to another session.
function Composer({
  busy,
  capabilities,
  onCancel,
  onError,
  onSubmit,
}: {
  busy: boolean;
  capabilities: PromptCapabilities;
  onCancel: () => void;
  onError: (message: string) => void;
  onSubmit: (prompt: PromptContentBlock[]) => void;
}) {
  const [text, setText] = useState("");
  const [attachments, setAttachments] = useState<File[]>([]);
  const [resourceLinkName, setResourceLinkName] = useState("");
  const [resourceLinkURI, setResourceLinkURI] = useState("");
  const files = useRef<HTMLInputElement>(null);
  const acceptsAttachments = capabilities.audio || capabilities.embeddedContext || capabilities.image;

  async function submit(): Promise<void> {
    let prompt: PromptContentBlock[];
    try {
      prompt = await promptBlocks(text, attachments, resourceLinkName, resourceLinkURI, capabilities);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Could not read attachment");
      return;
    }
    if (prompt.length === 0) {
      onError("Enter a prompt, choose an attachment, or add a resource link");
      return;
    }
    onSubmit(prompt);
    setText("");
    setAttachments([]);
    setResourceLinkName("");
    setResourceLinkURI("");
    if (files.current) {
      files.current.value = "";
    }
  }

  return (
    <section aria-labelledby="composer-heading">
      <h2 id="composer-heading">Prompt</h2>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <label>
          Message
          <textarea disabled={busy} onChange={(event) => setText(event.target.value)} value={text} />
        </label>
        <details>
          <summary>Add context</summary>
          {acceptsAttachments ? (
            <label>
              Attachments
              <input disabled={busy} multiple onChange={(event) => setAttachments(Array.from(event.target.files ?? []))} ref={files} type="file" />
            </label>
          ) : null}
          {attachments.length > 0 ? <p>{attachments.map((file) => file.name).join(", ")}</p> : null}
          <fieldset disabled={busy}>
            <legend>Resource link</legend>
            <label>Name<input onChange={(event) => setResourceLinkName(event.target.value)} value={resourceLinkName} /></label>
            <label>URI<input onChange={(event) => setResourceLinkURI(event.target.value)} type="url" value={resourceLinkURI} /></label>
          </fieldset>
        </details>
        <button disabled={busy} type="submit">
          Send prompt
        </button>
        {busy ? (
          <button onClick={onCancel} type="button">
            Cancel prompt
          </button>
        ) : null}
      </form>
    </section>
  );
}

function PermissionInteraction({ interaction, onPermission }: {
  interaction: Extract<PendingInteraction, { kind: "permission" }>;
  onPermission: (interactionId: string, optionId: string) => void;
}) {
  return (
    <article aria-label={`Permission for ${interaction.tool.title}`}>
      <h3>{interaction.tool.title}</h3>
      {interaction.tool.name ? <p>{interaction.tool.name}</p> : null}
      {interaction.tool.toolKind ? <p>{interaction.tool.toolKind}</p> : null}
      {interaction.options.map((option) => (
        <button key={option.id} onClick={() => onPermission(interaction.id, option.id)} type="button">
          {option.name}
        </button>
      ))}
    </article>
  );
}

function ElicitationForm({
  interaction,
  onSubmit,
}: {
  interaction: Extract<PendingInteraction, { kind: "form" }>;
  onSubmit: (interactionId: string, action: "accept" | "decline" | "cancel", content?: Record<string, FormValue>) => void;
}) {
  return (
    <article aria-label={interaction.title ?? interaction.message}>
      <h3>{interaction.title ?? "Question"}</h3>
      <p>{interaction.message}</p>
      {interaction.description ? <p>{interaction.description}</p> : null}
      <form onSubmit={(event) => {
        event.preventDefault();
        const form = new FormData(event.currentTarget);
        const content = Object.create(null) as Record<string, FormValue>;
        for (const field of interaction.fields) {
          switch (field.type) {
            case "string": {
              const value = form.get(field.name);
              if (value !== null && (field.required || value !== "")) content[field.name] = String(value);
              break;
            }
            case "number":
            case "integer": {
              const value = form.get(field.name);
              if (value !== null && value !== "") content[field.name] = Number(value);
              break;
            }
            case "boolean": content[field.name] = form.has(field.name); break;
            case "multi-select": content[field.name] = form.getAll(field.name).map(String); break;
          }
        }
        onSubmit(interaction.id, "accept", content);
      }}>
        {interaction.fields.map((field) => (
          <label key={field.name}>
            {field.label}
            {field.description ? <span>{field.description}</span> : null}
            {field.type === "boolean" ? (
              <input defaultChecked={field.default} name={field.name} type="checkbox" />
            ) : field.type === "multi-select" ? (
              <select defaultValue={field.default} multiple name={field.name} required={field.required}>
                {field.choices.map((choice) => <option key={choice.value} value={choice.value}>{choice.label}</option>)}
              </select>
            ) : field.type === "string" && field.choices ? (
              <select defaultValue={field.default ?? ""} name={field.name} required={field.required}>
                {field.default === undefined ? <option disabled value="">Select an option</option> : null}
                {field.choices.map((choice) => <option key={choice.value} value={choice.value}>{choice.label}</option>)}
              </select>
            ) : (
              <input defaultValue={field.default} max={field.type === "string" ? field.maxLength : field.maximum} min={field.type === "string" ? field.minLength : field.minimum} name={field.name} pattern={field.type === "string" ? field.pattern : undefined} required={field.required} step={field.type === "integer" ? 1 : undefined} type={field.type === "string" ? stringInputType(field.format) : "number"} />
            )}
          </label>
        ))}
        <button type="submit">Submit answer</button>
        <button onClick={() => onSubmit(interaction.id, "decline")} type="button">Decline</button>
        <button onClick={() => onSubmit(interaction.id, "cancel")} type="button">Cancel question</button>
      </form>
    </article>
  );
}

function stringInputType(format: "date" | "date-time" | "email" | "uri" | undefined): "date" | "datetime-local" | "email" | "text" | "url" {
  switch (format) {
    case "email": return "email";
    case "uri": return "url";
    case "date": return "date";
    case "date-time": return "datetime-local";
    default: return "text";
  }
}

function SessionInformation({ onConfigOption, transcript }: {
  onConfigOption: (configId: string, value: string) => void;
  transcript: SessionTranscript;
}) {
  if (transcript.configuration.length === 0 && !transcript.usage) return null;
  return (
    <section aria-label="Session information">
      {transcript.configuration.map((option) => (
        <label key={option.id}>
          {option.name}
          <select aria-label={option.name} onChange={(event) => onConfigOption(option.id, event.target.value)} value={option.currentValue}>
            {option.options.map((choice) => <option key={choice.value} value={choice.value}>{choice.name}</option>)}
          </select>
        </label>
      ))}
      {transcript.usage ? (
        <span>{`Context: ${transcript.usage.used} of ${transcript.usage.size} tokens used`}</span>
      ) : null}
    </section>
  );
}

function Transcript({ interactions, onElicitation, onPermission, transcript }: {
  interactions: PendingInteraction[];
  onElicitation: (interactionId: string, action: "accept" | "decline" | "cancel", content?: Record<string, FormValue>) => void;
  onPermission: (interactionId: string, optionId: string) => void;
  transcript: SessionTranscript;
}) {
  const inlinePermissions = (entry: Extract<SessionTranscript["entries"][number], { kind: "tool" }>) =>
    interactions.filter((interaction): interaction is Extract<PendingInteraction, { kind: "permission" }> =>
      interaction.kind === "permission" && entry.id === `tool:${interaction.tool.id}`,
    );
  const trailingInteractions = interactions.filter((interaction) =>
    interaction.kind === "form" || !transcript.entries.some((entry) =>
      entry.kind === "tool" && entry.id === `tool:${interaction.tool.id}`,
    ),
  );

  return (
    <section aria-labelledby="transcript-heading">
      <h2 id="transcript-heading">Transcript</h2>
      <ol aria-label="Session transcript">
        {transcript.entries.map((entry) => (
          <li key={entry.id}>
            {entry.kind === "tool" ? (
              <article aria-label={`Tool ${entry.title}`}>
                <h3>{entry.title}</h3>
                {entry.status ? <p>{entry.status}</p> : null}
                <details>
                  <summary>Details</summary>
                  {entry.name ? <p>{entry.name}</p> : null}
                  {entry.toolKind ? <p>{entry.toolKind}</p> : null}
                  {entry.locations.length > 0 ? <ul aria-label="Tool locations">{entry.locations.map((location) => <li key={`${location.path}:${location.line ?? ""}`}>{location.path}{location.line === undefined ? "" : `:${location.line}`}</li>)}</ul> : null}
                  {entry.content.map((content, index) => <ToolOutput content={content} key={index} />)}
                </details>
                {inlinePermissions(entry).map((interaction) => <PermissionInteraction interaction={interaction} key={interaction.id} onPermission={onPermission} />)}
              </article>
            ) : entry.kind === "unknown" ? (
              <details><summary>Unsupported transcript item</summary><p>{entry.label}</p></details>
            ) : entry.kind === "thought" ? (
              <details>
                <summary>Thought</summary>
                {entry.content.map((content, index) => <Content content={content} key={index} />)}
              </details>
            ) : (
              <article aria-label={`${entry.kind} message`}>
                <h3>{entry.kind === "agent" ? "Ox" : "You"}</h3>
                {entry.content.map((content, index) => <Content content={content} key={index} />)}
              </article>
            )}
          </li>
        ))}
        {trailingInteractions.map((interaction) => (
          <li key={interaction.id}>
            {interaction.kind === "permission" ? (
              <PermissionInteraction interaction={interaction} onPermission={onPermission} />
            ) : (
              <ElicitationForm interaction={interaction} onSubmit={onElicitation} />
            )}
          </li>
        ))}
      </ol>
      {transcript.plan.length > 0 ? (
        <details>
          <summary>{`Plan — ${transcript.plan.filter((entry) => entry.status === "completed").length} of ${transcript.plan.length} complete`}</summary>
          <ol>{transcript.plan.map((entry, index) => <li key={index}>{entry.content} ({entry.status})</li>)}</ol>
        </details>
      ) : null}
      {transcript.usage?.cost ? <details><summary>Cost</summary><p>{`${transcript.usage.cost.amount} ${transcript.usage.cost.currency}`}</p></details> : null}
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

// A browser file becomes the most faithful block the agent advertised support
// for, so an unsupported attachment is refused here rather than at the host.
async function promptBlocks(
  text: string,
  attachments: File[],
  resourceLinkName: string,
  resourceLinkURI: string,
  capabilities: PromptCapabilities,
): Promise<PromptContentBlock[]> {
  const prompt: PromptContentBlock[] = text ? [{ text, type: "text" }] : [];
  if (resourceLinkName || resourceLinkURI) {
    if (!resourceLinkName || !resourceLinkURI) {
      throw new Error("A resource link needs both a name and URI");
    }
    prompt.push({ name: resourceLinkName, type: "resource_link", uri: resourceLinkURI });
  }
  for (const file of attachments) {
    if (file.size > maximumAttachmentBytes) {
      throw new Error(`${file.name} is too large to attach`);
    }
    const uri = `attachment://local/${encodeURIComponent(file.name)}`;
    const mimeType = file.type ? { mimeType: file.type } : {};
    if (capabilities.image && file.type.startsWith("image/")) {
      prompt.push({ data: await fileData(file), mimeType: file.type, type: "image", uri });
    } else if (capabilities.audio && file.type.startsWith("audio/")) {
      prompt.push({ data: await fileData(file), mimeType: file.type, type: "audio" });
    } else if (!capabilities.embeddedContext) {
      throw new Error(`${file.name} is not supported by Ox`);
    } else if (isEmbeddableText(file)) {
      prompt.push({ resource: { ...mimeType, text: await file.text(), uri }, type: "resource" });
    } else {
      prompt.push({ resource: { blob: await fileData(file), ...mimeType, uri }, type: "resource" });
    }
  }
  return prompt;
}

function isEmbeddableText(file: File): boolean {
  const textual = file.type.startsWith("text/") || /(?:json|javascript|xml|yaml|toml)$/.test(file.type);
  return textual && file.size <= maximumPromptText;
}

async function fileData(file: File): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let data = "";
  for (let index = 0; index < bytes.length; index += 0x8000) {
    data += String.fromCharCode(...bytes.subarray(index, index + 0x8000));
  }
  return btoa(data);
}

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing application root");
}
createRoot(root).render(<App />);
