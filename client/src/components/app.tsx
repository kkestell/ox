import { ArrowDownIcon, ArrowLeftIcon, MessageSquarePlusIcon, MoreHorizontalIcon, SettingsIcon } from "lucide-react";
import { type ReactNode, useEffect, useLayoutEffect, useRef, useState } from "react";

import { type BrowserCommand, type MCPServer, type SessionTranscript } from "../protocol.ts";

import { AddWorkspaceDialog } from "./add-workspace-dialog.tsx";
import { Authentication } from "./authentication.tsx";
import { Composer } from "./composer.tsx";
import { type CommandResult, useHostConnection } from "./host-connection.ts";
import { MCPActivationForm } from "./mcp-activation-form.tsx";
import { PendingInteractions } from "./pending-interactions.tsx";
import { WorkspaceSidebar } from "./sidebar.tsx";
import { SupportDetails } from "./support-details.tsx";
import { Transcript } from "./transcript.tsx";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SidebarInset, SidebarProvider, SidebarTrigger, useSidebar } from "@/components/ui/sidebar";

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
  return (
    <SidebarProvider className="h-svh overflow-hidden">
      <Shell />
    </SidebarProvider>
  );
}

function Shell() {
  const { connection, send, snapshot } = useHostConnection();
  const { setOpenMobile } = useSidebar();
  const [credential, setCredential] = useState("");
  const [mcpServers, setMCPServers] = useState<MCPServer[]>([]);
  const [mcpMessage, setMCPMessage] = useState<string>();
  const [sessionError, setSessionError] = useState<string>();
  const [workspacePath, setWorkspacePath] = useState("");
  const [addingWorkspace, setAddingWorkspace] = useState(false);
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
    setOpenMobile(false);
    setSettings(true);
    if (workspaceId === selectedWorkspaceId) {
      return;
    }
    if (!send({ requestId: crypto.randomUUID(), type: "select-workspace", workspaceId })) {
      setWorkspaceMessage({ error: true, text: "The host connection is not open" });
    }
  }

  return (
    <>
      {snapshot ? (
        <WorkspaceSidebar
          onAdd={() => { setWorkspaceMessage(undefined); setAddingWorkspace(true); }}
          onClose={() => setOpenMobile(false)}
          onNew={(workspaceId) => {
            setOpenMobile(false);
            setSettings(false);
            submitSessionCommand({ requestId: crypto.randomUUID(), type: "new-conversation" }, workspaceId);
          }}
          onOlder={() => historyCommand("next-history-page")}
          onOpen={(workspaceId, sessionId) => {
            setOpenMobile(false);
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
      <SidebarInset className="min-w-0 overflow-hidden">
        {snapshot?.workspace && settings ? (
          <section aria-label="Workspace settings" className="flex min-h-0 flex-1 flex-col">
            <PrimaryHeader title="Workspace settings">
              <Button
                aria-label={active ? "Back to conversation" : "Back to workspace"}
                onClick={() => setSettings(false)}
                size="sm"
                variant="ghost"
              >
                <ArrowLeftIcon />
                <span className="hidden sm:inline">{active ? "Back to conversation" : "Back to workspace"}</span>
                <span className="sm:hidden">Back</span>
              </Button>
            </PrimaryHeader>
            <div className="min-h-0 flex-1 overflow-y-auto">
              <div className="mx-auto flex max-w-4xl flex-col gap-4 p-4 sm:p-6">
                <div>
                  <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Workspace</p>
                  <h2 className="mt-1 truncate text-xl font-semibold tracking-tight" title={snapshot.workspace.name}>
                    {snapshot.workspace.name}
                  </h2>
                </div>
                <Authentication
                  authentication={snapshot.authentication}
                  credential={credential}
                  onAuthenticate={(methodId) => sendRouted({ methodId, requestId: crypto.randomUUID(), type: "authenticate" })}
                  onCredential={setCredential}
                  onLogin={terminalLogin}
                  onLogout={() => sendRouted({ requestId: crypto.randomUUID(), type: "logout" })}
                />
                <Card asChild className="gap-4 py-4">
                  <section aria-labelledby="mcp-heading">
                    <CardHeader className="gap-1 px-4">
                      <CardTitle asChild>
                        <h3 id="mcp-heading">MCP servers</h3>
                      </CardTitle>
                      <CardDescription>{`${snapshot.workspace.mcpServerCount} configured`}</CardDescription>
                    </CardHeader>
                    <CardContent className="space-y-3 px-4">
                      {mcpMessage ? <p aria-live="polite" className="text-sm text-muted-foreground">{mcpMessage}</p> : null}
                      <MCPActivationForm onSave={saveMCPServers} servers={mcpServers} setServers={setMCPServers} />
                    </CardContent>
                  </section>
                </Card>
                <SupportDetails
                  activeSessionId={active?.id}
                  connection={connection}
                  onRefresh={() => historyCommand("refresh-history")}
                  snapshot={snapshot}
                  workspace={snapshot.workspace}
                />
              </div>
            </div>
          </section>
        ) : snapshot?.workspace && !authenticated && !unavailable ? (
          <section className="flex min-h-0 flex-1 flex-col">
            <PrimaryHeader title="Ox" />
            <EmptyState
              action="Open workspace settings"
              description="Connect this workspace before starting a conversation."
              icon={<SettingsIcon />}
              onAction={() => setSettings(true)}
              title="Connect Ox"
            />
          </section>
        ) : snapshot?.workspace && active ? (
          <section aria-label="Conversation" className="flex min-h-0 flex-1 flex-col">
            <PrimaryHeader title={title}>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button aria-label="Conversation actions" size="icon-sm" variant="ghost">
                    <MoreHorizontalIcon />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onSelect={() => conversationCommand("close-conversation", active.id)}>
                    Close conversation
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onSelect={() => conversationCommand("delete-conversation", active.id)}
                    variant="destructive"
                  >
                    Delete conversation
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </PrimaryHeader>
            <TranscriptScroll
              sessionError={sessionError}
              sessionId={active.id}
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
            <footer className="shrink-0 bg-background">
              <div className="mx-auto w-full max-w-3xl px-4 py-4">
                <div className="w-full">
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
                </div>
              </div>
            </footer>
          </section>
        ) : snapshot?.workspace ? (
          <section className="flex min-h-0 flex-1 flex-col">
            <PrimaryHeader title="Ox" />
            <div className="flex min-h-0 flex-1 flex-col overflow-y-auto p-4">
              {unavailable ? (
                <Alert className="mx-auto mb-4 w-full max-w-2xl" variant="destructive">
                  <AlertDescription>Ox is unavailable. Open workspace settings for diagnostics.</AlertDescription>
                </Alert>
              ) : null}
              {sessionError ? (
                <Alert className="mx-auto mb-4 w-full max-w-2xl" variant="destructive">
                  <AlertDescription>{sessionError}</AlertDescription>
                </Alert>
              ) : null}
              <EmptyState
                action={unavailable ? "Open workspace settings" : "New conversation"}
                description={unavailable ? "Review diagnostics or restart Ox for this workspace." : "Start a conversation or choose one from the navigation."}
                icon={unavailable ? <SettingsIcon /> : <MessageSquarePlusIcon />}
                onAction={() => unavailable ? setSettings(true) : selectedWorkspaceId && submitSessionCommand({ requestId: crypto.randomUUID(), type: "new-conversation" }, selectedWorkspaceId)}
                title={unavailable ? "Workspace unavailable" : "Ready when you are"}
              />
            </div>
          </section>
        ) : (
          <section className="flex min-h-0 flex-1 flex-col">
            <PrimaryHeader title="Ox" />
            <EmptyState
              action="Add workspace"
              description="Add a folder on this server to start using Ox."
              icon={<MessageSquarePlusIcon />}
              onAction={() => setAddingWorkspace(true)}
              title="No workspace selected"
            />
          </section>
        )}
      </SidebarInset>
      <AddWorkspaceDialog
        message={workspaceMessage}
        onClose={() => setAddingWorkspace(false)}
        onPath={setWorkspacePath}
        onRegister={() => submitWorkspaceCommand({ path: workspacePath, requestId: crypto.randomUUID(), type: "register-workspace" })}
        open={addingWorkspace}
        path={workspacePath}
      />
    </>
  );
}

function PrimaryHeader({ children, title }: { children?: ReactNode; title: string }) {
  return (
    <header className="flex h-14 shrink-0 items-center gap-2 border-b px-3 sm:px-4">
      <SidebarTrigger aria-label="Open navigation" className="md:hidden" />
      <h2 className="min-w-0 flex-1 truncate text-base font-semibold tracking-tight" title={title}>
        {title}
      </h2>
      {children}
    </header>
  );
}

function EmptyState({ action, description, icon, onAction, title }: {
  action: string;
  description: string;
  icon: ReactNode;
  onAction: () => void;
  title: string;
}) {
  return (
    <div className="flex min-h-64 flex-1 items-center justify-center px-6 py-12 text-center">
      <div className="max-w-sm">
        <span className="mx-auto flex size-11 items-center justify-center rounded-xl border bg-card text-muted-foreground shadow-sm [&_svg]:size-5">
          {icon}
        </span>
        <h2 className="mt-4 text-lg font-semibold tracking-tight">{title}</h2>
        <p className="mt-1 text-sm leading-6 text-muted-foreground">{description}</p>
        <Button className="mt-5" onClick={onAction}>{action}</Button>
      </div>
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
  const pointerScrolling = useRef(false);
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

  const latestUserEntry = transcript.entries.findLast((entry) => entry.kind === "user");
  useLayoutEffect(() => {
    if (!latestUserEntry) return;
    following.current = true;
    setShowJump(false);
    scrollToLatest();
  }, [latestUserEntry?.id]);

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

  function stopFollowing(): void {
    following.current = false;
    setShowJump(true);
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div
        className="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain"
        data-testid="transcript-scrollport"
        onKeyDownCapture={(event) => {
          if (["ArrowUp", "Home", "PageUp"].includes(event.key)) stopFollowing();
          if (event.key === "End") {
            following.current = true;
            setShowJump(false);
          }
        }}
        onPointerDown={() => {
          pointerScrolling.current = true;
        }}
        onPointerUp={() => {
          updateFollowing();
          pointerScrolling.current = false;
        }}
        onScroll={() => {
          if (pointerScrolling.current || !following.current) updateFollowing();
          else scrollToLatest();
        }}
        onWheel={(event) => {
          if (event.deltaY < 0) stopFollowing();
          else requestAnimationFrame(updateFollowing);
        }}
        ref={scrollport}
      >
        <div className="mx-auto flex min-h-full min-w-0 max-w-3xl flex-col gap-4 px-4 py-5" ref={content}>
          {unavailable ? (
            <Alert variant="destructive">
              <AlertDescription>Ox is unavailable. Open workspace settings for diagnostics.</AlertDescription>
            </Alert>
          ) : null}
          {sessionError ? (
            <Alert variant="destructive">
              <AlertDescription>{sessionError}</AlertDescription>
            </Alert>
          ) : null}
          <Transcript key={sessionId} transcript={transcript} />
        </div>
      </div>
      {showJump ? (
        <div className="flex shrink-0 justify-center bg-background/95 px-3 py-2 backdrop-blur">
          <Button
            onClick={() => {
              following.current = true;
              setShowJump(false);
              scrollToLatest();
            }}
            size="sm"
            variant="outline"
          >
            <ArrowDownIcon />
            Jump to latest
          </Button>
        </div>
      ) : null}
    </div>
  );
}
