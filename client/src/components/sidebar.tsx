import {
  CircleIcon,
  LockIcon,
  MoreHorizontalIcon,
  PlusIcon,
  RotateCcwIcon,
  SettingsIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";

import { type Snapshot } from "../protocol.ts";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
} from "@/components/ui/sidebar";

// Navigating between conversations is a link, so the sidebar reads as
// navigation; every control that changes host state stays a button.
export function WorkspaceSidebar({ onAdd, onClose, onNew, onOlder, onOpen, onRemove, onRestart, onSettings, sessions, settings, workspaces }: {
  onAdd: () => void;
  onClose: () => void;
  onNew: (workspaceId: string) => void;
  onOlder: () => void;
  onOpen: (workspaceId: string, sessionId: string) => void;
  onRemove: (workspaceId: string) => void;
  onRestart: (workspaceId: string) => void;
  onSettings: (workspaceId: string) => void;
  sessions: Snapshot["sessions"];
  settings: boolean;
  workspaces: Snapshot["workspaces"];
}) {
  return (
    <Sidebar>
      <nav aria-label="Workspaces and conversations" className="flex h-full min-h-0 flex-col">
        <SidebarHeader className="h-14 flex-row items-center gap-2 border-b px-3">
          <h1 className="flex-1 text-base font-semibold tracking-tight">Ox</h1>
          <Button
            aria-label="Add workspace"
            onClick={onAdd}
            size="icon-sm"
            title="Add workspace"
            variant="ghost"
          >
            <PlusIcon />
          </Button>
          <Button
            aria-label="Close navigation"
            className="md:hidden"
            onClick={onClose}
            size="icon-sm"
            title="Close navigation"
            variant="ghost"
          >
            <XIcon />
          </Button>
        </SidebarHeader>
        <SidebarContent>
          {workspaces.values.length > 0 ? (
            <SidebarGroup className="px-2 py-3">
              <SidebarMenu aria-label="Workspaces" className="gap-6">
                {workspaces.values.map((workspace) => (
                  <SidebarMenuItem
                    aria-current={workspaces.selectedId === workspace.id ? "true" : undefined}
                    className="min-w-0"
                    key={workspace.id}
                  >
                    {/* The trailing controls end where the sidebar header's button does:
                        the list group's 8px plus this row's 4px make the same 12px inset. */}
                    <div className="flex min-w-0 items-center gap-1 pl-2 pr-1">
                      <h2 className="min-w-0 flex-1 truncate text-sm font-semibold" title={workspace.name}>
                        {workspace.name}
                      </h2>
                      {workspace.status === "ready" ? (
                        <Button
                          aria-label={`New conversation in ${workspace.name}`}
                          onClick={() => onNew(workspace.id)}
                          size="icon-sm"
                          title="New conversation"
                          variant="ghost"
                        >
                          <PlusIcon />
                        </Button>
                      ) : null}
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            aria-label={`Workspace actions for ${workspace.name}`}
                            size="icon-sm"
                            title="Workspace actions"
                            variant="ghost"
                          >
                            <MoreHorizontalIcon />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem asChild>
                            <a
                              aria-label={`Settings for ${workspace.name}`}
                              aria-current={settings && workspaces.selectedId === workspace.id ? "page" : undefined}
                              href={`#settings-${workspace.id}`}
                              onClick={() => onSettings(workspace.id)}
                            >
                              <SettingsIcon />
                              Workspace settings
                            </a>
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem aria-label={`Remove ${workspace.name}`} onSelect={() => onRemove(workspace.id)} variant="destructive">
                            <Trash2Icon />
                            Remove workspace
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                    {workspace.status === "ready" ? (
                      <>
                        <SidebarMenuSub
                          aria-label={`${workspace.name} conversations`}
                          className="mx-0 mt-1 gap-1 translate-x-0 border-l-0 pr-0 pl-0"
                        >
                          {workspace.conversations.map((conversation) => (
                            <SidebarMenuSubItem key={conversation.id}>
                              <SidebarMenuSubButton
                                asChild
                                className="h-auto min-w-0 flex-col items-start gap-1 rounded-lg px-2 py-2"
                                isActive={!settings && sessions.selectedId === conversation.id}
                              >
                                <a
                                  aria-disabled={conversation.status === "locked" || undefined}
                                  aria-current={!settings && sessions.selectedId === conversation.id ? "page" : undefined}
                                  href={conversation.status === "locked" ? undefined : `#${conversation.id}`}
                                  onClick={conversation.status === "locked" ? undefined : () => onOpen(workspace.id, conversation.id)}
                                  title={conversation.status === "locked" ? "Open in another client" : undefined}
                                >
                                  <span className="flex w-full min-w-0 items-center gap-1.5">
                                    {conversation.status === "locked" ? (
                                      <LockIcon aria-label="Open in another client" className="size-3.5 shrink-0 text-muted-foreground" />
                                    ) : null}
                                    {conversation.awaiting ? (
                                      <CircleIcon aria-label="Waiting for you" className="size-2 shrink-0 fill-amber-500 text-amber-500" />
                                    ) : null}
                                    <span className="min-w-0 truncate font-medium" title={conversation.title ?? "Untitled conversation"}>
                                      {conversation.title ?? "Untitled conversation"}
                                    </span>
                                  </span>
                                  {conversation.status === "loading" || conversation.updatedAt ? (
                                    <span className="flex w-full min-w-0 items-center gap-2 text-xs text-sidebar-foreground/55">
                                      {conversation.updatedAt ? (
                                        <time className="min-w-0 flex-1 truncate" dateTime={conversation.updatedAt}>
                                          {shortDate(conversation.updatedAt)}
                                        </time>
                                      ) : null}
                                      {conversation.status === "loading" ? <span className="shrink-0">Opening…</span> : null}
                                    </span>
                                  ) : null}
                                </a>
                              </SidebarMenuSubButton>
                            </SidebarMenuSubItem>
                          ))}
                        </SidebarMenuSub>
                        {workspaces.selectedId === workspace.id && sessions.nextCursor ? (
                          <Button
                            className="mt-1 w-full justify-start text-muted-foreground"
                            onClick={onOlder}
                            size="sm"
                            variant="ghost"
                          >
                            Show older conversations
                          </Button>
                        ) : null}
                      </>
                    ) : (
                      <div className="mt-2 flex items-center gap-2 rounded-lg border border-dashed bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
                        <span className="min-w-0 flex-1">{workspace.status === "starting" ? "Ox is starting…" : "Ox is not running."}</span>
                        {workspace.status === "starting" ? null : (
                          <Button
                            aria-label={`Restart ${workspace.name}`}
                            onClick={() => onRestart(workspace.id)}
                            size="xs"
                            variant="outline"
                          >
                            <RotateCcwIcon />
                            Restart
                          </Button>
                        )}
                      </div>
                    )}
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            </SidebarGroup>
          ) : (
            <div className="px-4 py-6 text-center">
              <p className="text-sm font-medium">No workspaces yet</p>
              <p className="mt-1 text-sm text-muted-foreground">Add a folder on this server to start a conversation.</p>
            </div>
          )}
        </SidebarContent>
      </nav>
    </Sidebar>
  );
}

function shortDate(value: string): string {
  return new Date(value).toLocaleString(undefined, {
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    month: "short",
  });
}
