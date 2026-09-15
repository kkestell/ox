import { useEffect, useState } from "react";

import { type BrowserCommand, type MCPServer } from "../protocol.ts";

import { Authentication } from "./authentication.tsx";
import { Composer } from "./composer.tsx";
import { Disclosure } from "./disclosure.tsx";
import { type CommandResult, useHostConnection } from "./host-connection.ts";
import { MCPActivationForm } from "./mcp-activation-form.tsx";
import { SessionInformation } from "./session-information.tsx";
import { Sidebar } from "./sidebar.tsx";
import { SupportDetails } from "./support-details.tsx";
import { Transcript } from "./transcript.tsx";

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

export function App() {
  const { connection, send, snapshot } = useHostConnection();
  const [credential, setCredential] = useState("");
  const [mcpServers, setMCPServers] = useState<MCPServer[]>([]);
  const [mcpMessage, setMCPMessage] = useState<string>();
  const [sessionError, setSessionError] = useState<string>();
  const [workspacePath, setWorkspacePath] = useState("");
  const [settings, setSettings] = useState(false);
  const [workspaceMessage, setWorkspaceMessage] = useState<{ error: boolean; text: string }>();
  const active = snapshot?.sessions.active;

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

  // Returns why the command could not be sent, so a missing selection is not
  // reported as a lost host connection. The sidebar acts on a named workspace,
  // which is how opening or creating a conversation elsewhere stays one command.
  function sendRouted(
    request: RoutedRequest,
    named?: string,
    onResult?: (result: CommandResult) => void,
  ): string | undefined {
    const workspaceId = named ?? snapshot?.workspaces.selectedId;
    if (workspaceId === undefined) {
      return "No workspace is selected";
    }
    return send({ ...request, workspaceId } as RoutedCommand, onResult) ? undefined : "The host connection is not open";
  }

  // Session commands report their outcome only through their result, so the
  // browser keeps the latest failure visible until another one succeeds.
  function submitSessionCommand(request: RoutedRequest, named?: string): void {
    const problem = sendRouted(request, named, (result) => setSessionError(result.ok ? undefined : result.error));
    if (problem !== undefined) {
      setSessionError(problem);
    }
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
    const problem = sendRouted(
      { mcpServers: servers, requestId: crypto.randomUUID(), type: "set-mcp-servers" },
      undefined,
      (result) => {
        if (result.ok) {
          setMCPServers([]);
        }
        setMCPMessage(result.ok ? "MCP servers saved" : result.error);
      },
    );
    setMCPMessage(problem ?? "Saving MCP servers…");
  }

  function submitWorkspaceCommand(command: BrowserCommand): void {
    const sent = send(command, (result) => {
      if (!result.ok) {
        setWorkspaceMessage({ error: true, text: result.error });
        return;
      }
      if (command.type === "register-workspace") {
        setWorkspacePath("");
      }
      setWorkspaceMessage({
        error: false,
        text: command.type === "restart-workspace" ? "Ox restarted" : "Workspace registry updated",
      });
    });
    if (!sent) {
      setWorkspaceMessage({ error: true, text: "The host connection is not open" });
      return;
    }
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
              <Disclosure label="Conversation actions">
                <button onClick={() => conversationCommand("close-conversation", active.id)} type="button">Close conversation</button>
                <button onClick={() => conversationCommand("delete-conversation", active.id)} type="button">Delete conversation</button>
              </Disclosure>
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
