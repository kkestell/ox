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

function App() {
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [connection, setConnection] = useState("Connecting");
  const socket = useRef<WebSocket | undefined>(undefined);
  const [credential, setCredential] = useState("");
  const [mcpServers, setMCPServers] = useState<MCPServer[]>([]);
  const [mcpMessage, setMCPMessage] = useState<string>();
  const [sessionError, setSessionError] = useState<string>();
  const [workspacePath, setWorkspacePath] = useState("");
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
          setWorkspaceMessage({ error: false, text: "Workspace registry updated" });
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
    if (!send({ mcpServers: servers, requestId, type: "set-mcp-servers" })) {
      setMCPMessage("The host connection is not open");
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

  const selected = snapshot?.sessions.values.find((session) => session.id === snapshot.sessions.selectedId);
  const title = selected?.title ?? "New conversation";
  const authenticated = snapshot?.authentication.status === "authenticated";
  const unavailable = connection === "Unavailable" || connection === "Disconnected" || snapshot?.workspace?.status === "unavailable";

  return (
    <main>
      <header>
        <h1>Ox</h1>
        {snapshot ? <p>{snapshot.workspace?.name ?? "No workspace selected"}</p> : null}
      </header>
      {unavailable ? <p role="alert">Ox is unavailable. Open support details for diagnostics.</p> : null}
      {snapshot ? (
        <WorkspacePicker
          message={workspaceMessage}
          onPath={setWorkspacePath}
          onRegister={() =>
            submitWorkspaceCommand({ path: workspacePath, requestId: crypto.randomUUID(), type: "register-workspace" })
          }
          onRemove={(workspaceId) =>
            submitWorkspaceCommand({ requestId: crypto.randomUUID(), type: "remove-workspace", workspaceId })
          }
          onSelect={(workspaceId) =>
            submitWorkspaceCommand({ requestId: crypto.randomUUID(), type: "select-workspace", workspaceId })
          }
          path={workspacePath}
          workspaces={snapshot.workspaces}
        />
      ) : null}
      {snapshot?.workspace && !authenticated ? (
        <Authentication
          authentication={snapshot.authentication}
          credential={credential}
          onAuthenticate={(methodId) => send({ methodId, requestId: crypto.randomUUID(), type: "authenticate" })}
          onCredential={setCredential}
          onLogin={terminalLogin}
        />
      ) : null}
      {snapshot?.workspace && authenticated ? (
        <History
          onNew={() => submitSessionCommand({ requestId: crypto.randomUUID(), type: "new-conversation" })}
          onOpen={(sessionId) => conversationCommand("open-conversation", sessionId)}
          onOlder={() => historyCommand("next-history-page")}
          sessions={snapshot.sessions}
        />
      ) : null}
      {sessionError ? <p role="alert">{sessionError}</p> : null}
      {snapshot?.workspace && authenticated && active ? (
        <section aria-label="Conversation">
          <header>
            <h2>{title}</h2>
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
      ) : authenticated ? (
        <p>Choose a conversation or start a new one.</p>
      ) : null}
      {snapshot?.workspace ? (
        <details>
          <summary>Workspace settings</summary>
          <details>
            <summary>{`MCP servers — ${snapshot.workspace.mcpServerCount} configured`}</summary>
            {mcpMessage ? <p aria-live="polite">{mcpMessage}</p> : null}
            <MCPActivationForm onSave={saveMCPServers} servers={mcpServers} setServers={setMCPServers} />
          </details>
          <SupportDetails
            activeSessionId={active?.id}
            connection={connection}
            credential={credential}
            onCredential={setCredential}
            onLogin={terminalLogin}
            onLogout={() => send({ requestId: crypto.randomUUID(), type: "logout" })}
            onRefresh={() => historyCommand("refresh-history")}
            snapshot={snapshot}
            workspace={snapshot.workspace}
          />
        </details>
      ) : null}
    </main>
  );
}

function WorkspacePicker({ message, onPath, onRegister, onRemove, onSelect, path, workspaces }: {
  message?: { error: boolean; text: string };
  onPath: (path: string) => void;
  onRegister: () => void;
  onRemove: (workspaceId: string) => void;
  onSelect: (workspaceId: string) => void;
  path: string;
  workspaces: Snapshot["workspaces"];
}) {
  const selectedID = workspaces.selectedId;
  return (
    <section aria-labelledby="workspaces-heading">
      <h2 id="workspaces-heading">Workspaces</h2>
      {workspaces.values.length > 0 ? (
        <>
          <label>
            Current workspace
            <select
              onChange={(event) => onSelect(event.target.value)}
              value={selectedID}
            >
              {workspaces.values.map((workspace) => <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}
            </select>
          </label>
          {selectedID ? <button onClick={() => onRemove(selectedID)} type="button">Remove workspace</button> : null}
        </>
      ) : <p>Register a server-local workspace to begin.</p>}
      <form onSubmit={(event) => { event.preventDefault(); onRegister(); }}>
        <label>
          Workspace path
          <input onChange={(event) => onPath(event.target.value)} required value={path} />
        </label>
        <button type="submit">Register workspace</button>
      </form>
      {message ? <p aria-live="polite" role={message.error ? "alert" : undefined}>{message.text}</p> : null}
    </section>
  );
}

function Authentication({ authentication, credential, onAuthenticate, onCredential, onLogin }: {
  authentication: Snapshot["authentication"];
  credential: string;
  onAuthenticate: (methodId: string) => void;
  onCredential: (credential: string) => void;
  onLogin: (methodId: string) => void;
}) {
  return (
    <section aria-labelledby="authentication-heading">
      <h2 id="authentication-heading">Connect OpenRouter</h2>
      {authentication.error ? <p role="alert">{authentication.error}</p> : null}
      {authentication.methods.map((method) => method.type === "agent" ? (
        <button disabled={authentication.status === "working"} key={method.id} onClick={() => onAuthenticate(method.id)} type="button">Use configured credential</button>
      ) : (
        <form key={method.id} onSubmit={(event) => { event.preventDefault(); onLogin(method.id); }}>
          <label>OpenRouter API key<input autoComplete="off" disabled={authentication.status === "working"} onChange={(event) => onCredential(event.target.value)} required type="password" value={credential} /></label>
          <button disabled={authentication.status === "working"} type="submit">Save credential</button>
        </form>
      ))}
    </section>
  );
}

function History({ onNew, onOpen, onOlder, sessions }: {
  onNew: () => void;
  onOpen: (sessionId: string) => void;
  onOlder: () => void;
  sessions: Snapshot["sessions"];
}) {
  return (
    <nav aria-label="Conversations">
      <h2>Conversations</h2>
      <button onClick={onNew} type="button">New conversation</button>
      <ul aria-label="Conversation history">
        {sessions.values.map((conversation) => (
          <li key={conversation.id}>
            <button aria-current={sessions.selectedId === conversation.id ? "page" : undefined} onClick={() => onOpen(conversation.id)} type="button">
              {conversation.title ?? "Untitled conversation"}
            </button>
            {conversation.status === "loading" ? <span>Opening…</span> : null}
            {conversation.updatedAt ? <time dateTime={conversation.updatedAt}>{new Date(conversation.updatedAt).toLocaleString()}</time> : null}
          </li>
        ))}
      </ul>
      {sessions.nextCursor ? <button onClick={onOlder} type="button">Show older conversations</button> : null}
    </nav>
  );
}

function SupportDetails({ activeSessionId, connection, credential, onCredential, onLogin, onLogout, onRefresh, snapshot, workspace }: {
  activeSessionId?: string;
  connection: string;
  credential: string;
  onCredential: (credential: string) => void;
  onLogin: (methodId: string) => void;
  onLogout: () => void;
  onRefresh: () => void;
  snapshot: Snapshot;
  workspace: NonNullable<Snapshot["workspace"]>;
}) {
  return (
    <details>
      <summary>Support details</summary>
      <dl>
        <dt>Browser connection</dt><dd>{connection}</dd>
        <dt>Ox process</dt><dd>{workspace.status}</dd>
        <dt>Host revision</dt><dd>{snapshot.revision}</dd>
        {activeSessionId ? <><dt>Session ID</dt><dd>{activeSessionId}</dd></> : null}
      </dl>
      {workspace.diagnostics.length > 0 ? <ul aria-label="Workspace diagnostics">{workspace.diagnostics.map((diagnostic, index) => <li key={`${index}-${diagnostic}`}>{diagnostic}</li>)}</ul> : null}
      <button onClick={onRefresh} type="button">Refresh conversation history</button>
      <details>
        <summary>Authentication</summary>
        {snapshot.authentication.error ? <p role="alert">{snapshot.authentication.error}</p> : null}
        {snapshot.authentication.methods.map((method) => method.type === "terminal" ? (
          <form key={method.id} onSubmit={(event) => { event.preventDefault(); onLogin(method.id); }}>
            <label>{`${method.name} credential`}<input autoComplete="off" disabled={snapshot.authentication.status === "working"} onChange={(event) => onCredential(event.target.value)} required type="password" value={credential} /></label>
            <button disabled={snapshot.authentication.status === "working"} type="submit">{method.name}</button>
          </form>
        ) : null)}
        {snapshot.authentication.logoutAvailable ? <button disabled={snapshot.authentication.status === "working"} onClick={onLogout} type="button">Log out</button> : null}
      </details>
    </details>
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
