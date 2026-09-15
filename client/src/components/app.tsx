import { useEffect, useLayoutEffect, useRef, useState } from "react";

import { type BrowserCommand, type MCPServer, type PendingInteraction, type SessionTranscript } from "../protocol.ts";

import { AddWorkspaceDialog } from "./add-workspace-dialog.tsx";
import { Authentication } from "./authentication.tsx";
import { Composer } from "./composer.tsx";
import { Disclosure } from "./disclosure.tsx";
import { type CommandResult, useHostConnection } from "./host-connection.ts";
import { MCPActivationForm } from "./mcp-activation-form.tsx";
import { PendingInteractions } from "./pending-interactions.tsx";
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
  const [addingWorkspace, setAddingWorkspace] = useState(false);
  const [navigationOpen, setNavigationOpen] = useState(false);
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

  useEffect(() => {
    if (!navigationOpen) return;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setNavigationOpen(false);
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [navigationOpen]);

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
        setAddingWorkspace(false);
        setWorkspaceMessage(undefined);
        return;
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
    setNavigationOpen(false);
    setSettings(true);
    if (workspaceId === selectedWorkspaceId) {
      return;
    }
    if (!send({ requestId: crypto.randomUUID(), type: "select-workspace", workspaceId })) {
      setWorkspaceMessage({ error: true, text: "The host connection is not open" });
    }
  }

  return (
    <div className="app" data-navigation={navigationOpen ? "open" : undefined}>
      <header className="bar">
        <button aria-expanded={navigationOpen} aria-label="Open navigation" className="icon" onClick={() => setNavigationOpen(true)} type="button">
          <MenuIcon />
        </button>
        <h1>Ox</h1>
      </header>
      {snapshot ? (
        <Sidebar
          onAdd={() => { setWorkspaceMessage(undefined); setAddingWorkspace(true); }}
          onClose={() => setNavigationOpen(false)}
          onNew={(workspaceId) => {
            setNavigationOpen(false);
            setSettings(false);
            submitSessionCommand({ requestId: crypto.randomUUID(), type: "new-conversation" }, workspaceId);
          }}
          onOlder={() => historyCommand("next-history-page")}
          onOpen={(workspaceId, sessionId) => {
            setNavigationOpen(false);
            setSettings(false);
            submitSessionCommand({ requestId: crypto.randomUUID(), sessionId, type: "open-conversation" }, workspaceId);
          }}
          onRemove={(workspaceId) => submitWorkspaceCommand({ requestId: crypto.randomUUID(), type: "remove-workspace", workspaceId })}
          onRestart={restartWorkspace}
          onSettings={showSettings}
          sessions={snapshot.sessions}
          settings={settings}
          workspaces={snapshot.workspaces}
        />
      ) : null}
      <main inert={navigationOpen ? true : undefined}>
        {snapshot?.workspace && settings ? (
          <div className="scroll">
            <section aria-label="Workspace settings" className="settings">
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
          </div>
        ) : snapshot?.workspace && !authenticated && !unavailable ? (
          <div className="scroll">
            <p>{"Ox is not connected. Connect it in "}<a href={`#settings-${selectedWorkspaceId}`} onClick={() => setSettings(true)}>workspace settings</a>.</p>
          </div>
        ) : snapshot?.workspace && active ? (
          <section aria-label="Conversation" className="conversation">
            <header>
              <h2 className="truncate">{title}</h2>
              <Disclosure label="Conversation actions">
                <button onClick={() => conversationCommand("close-conversation", active.id)} type="button">Close conversation</button>
                <button onClick={() => conversationCommand("delete-conversation", active.id)} type="button">Delete conversation</button>
              </Disclosure>
            </header>
            <TranscriptScroll
              sessionId={active.id}
              sessionError={sessionError}
              transcript={active.transcript}
              unavailable={unavailable}
            />
            <PendingInteractions
              interactions={active.interactions}
              onElicitation={(interactionId, action, content) =>
                submitSessionCommand({ action, ...(content === undefined ? {} : { content }), interactionId, requestId: crypto.randomUUID(), sessionId: active.id, type: "resolve-elicitation" })
              }
              onPermission={(interactionId, optionId) =>
                submitSessionCommand({ interactionId, optionId, requestId: crypto.randomUUID(), sessionId: active.id, type: "resolve-permission" })
              }
            />
            <footer>
              <Composer
                busy={active.busy}
                capabilities={snapshot.workspace.promptCapabilities}
                key={active.id}
                onCancel={() => submitSessionCommand({ requestId: crypto.randomUUID(), sessionId: active.id, type: "cancel-prompt" })}
                onConfigOption={(configId, value) =>
                  submitSessionCommand({ configId, requestId: crypto.randomUUID(), sessionId: active.id, type: "set-config-option", value })
                }
                onError={setSessionError}
                onSubmit={(prompt) => submitSessionCommand({ prompt, requestId: crypto.randomUUID(), sessionId: active.id, type: "prompt" })}
                transcript={active.transcript}
              />
            </footer>
          </section>
        ) : snapshot?.workspace ? (
          <div className="scroll">
            {unavailable ? <p role="alert">Ox is unavailable. Open workspace settings for diagnostics.</p> : null}
            {sessionError ? <p role="alert">{sessionError}</p> : null}
            <p>Choose a conversation or start a new one.</p>
          </div>
        ) : null}
      </main>
      <AddWorkspaceDialog
        message={workspaceMessage}
        onClose={() => setAddingWorkspace(false)}
        onPath={setWorkspacePath}
        onRegister={() => submitWorkspaceCommand({ path: workspacePath, requestId: crypto.randomUUID(), type: "register-workspace" })}
        open={addingWorkspace}
        path={workspacePath}
      />
    </div>
  );
}

const followThreshold = 40;

// This is browser-only presentation state. The host continues to own the
// transcript; selecting a conversation always starts its view at the newest item.
function TranscriptScroll({
  sessionError,
  sessionId,
  transcript,
  unavailable,
}: {
  sessionError?: string;
  sessionId: string;
  transcript: SessionTranscript;
  unavailable: boolean;
}) {
  const scrollport = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const [showJump, setShowJump] = useState(false);

  function scrollToLatest(): void {
    const element = scrollport.current;
    if (!element) return;
    element.scrollTop = element.scrollHeight;
  }

  useLayoutEffect(() => {
    following.current = true;
    setShowJump(false);
    scrollToLatest();
  }, [sessionId]);

  useEffect(() => {
    const element = scrollport.current;
    const rendered = content.current;
    if (!element || !rendered) return;
    let frame: number | undefined;
    const stick = () => {
      if (!following.current) return;
      cancelAnimationFrame(frame ?? 0);
      frame = requestAnimationFrame(scrollToLatest);
    };
    const mutations = new MutationObserver(stick);
    const resize = new ResizeObserver(stick);
    mutations.observe(rendered, { characterData: true, childList: true, subtree: true });
    resize.observe(element);
    resize.observe(rendered);
    stick();
    return () => {
      mutations.disconnect();
      resize.disconnect();
      if (frame !== undefined) cancelAnimationFrame(frame);
    };
  }, [sessionId]);

  function updateFollowing(): void {
    const element = scrollport.current;
    if (!element) return;
    const atLatest = element.scrollHeight - element.scrollTop - element.clientHeight <= followThreshold;
    following.current = atLatest;
    setShowJump(!atLatest);
  }

  return (
    <div className="scroll transcript-scroll" data-testid="transcript-scrollport" onScroll={updateFollowing} ref={scrollport}>
      <div className="transcript-content" ref={content}>
        {unavailable ? <p role="alert">Ox is unavailable. Open workspace settings for diagnostics.</p> : null}
        {sessionError ? <p role="alert">{sessionError}</p> : null}
        <Transcript transcript={transcript} />
      </div>
      {showJump ? (
        <button className="jump-to-latest" onClick={() => {
          following.current = true;
          setShowJump(false);
          scrollToLatest();
        }} type="button">
          Jump to latest
        </button>
      ) : null}
    </div>
  );
}

function MenuIcon() {
  return <svg aria-hidden="true" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="2" viewBox="0 0 24 24"><path d="M4 6h16M4 12h16M4 18h16" /></svg>;
}
