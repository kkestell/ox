import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

import { type Snapshot } from "../protocol.ts";

// Navigating between conversations is a link, so the sidebar reads as
// navigation; every control that changes host state stays a button.
export function Sidebar({ message, onNew, onOlder, onOpen, onPath, onRegister, onRemove, onRestart, onSettings, path, sessions, settings, workspaces }: {
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
    <nav aria-label="Workspaces and conversations" className="w-72 shrink-0 overflow-y-auto">
      <h1>Ox</h1>
      {workspaces.values.length > 0 ? (
        <ul aria-label="Workspaces">
          {workspaces.values.map((workspace) => (
            <li aria-current={workspaces.selectedId === workspace.id ? "true" : undefined} key={workspace.id}>
              <h2>{workspace.name}</h2>
              <Button aria-label={`New conversation in ${workspace.name}`} onClick={() => onNew(workspace.id)} type="button">+</Button>
              <a
                aria-current={settings && workspaces.selectedId === workspace.id ? "page" : undefined}
                aria-label={`Settings for ${workspace.name}`}
                href={`#settings-${workspace.id}`}
                onClick={() => onSettings(workspace.id)}
              >
                Settings
              </a>
              <Button aria-label={`Remove ${workspace.name}`} onClick={() => onRemove(workspace.id)} type="button">Remove</Button>
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
                    <Button onClick={onOlder} type="button">Show older conversations</Button>
                  ) : null}
                </>
              ) : (
                <p>
                  {workspace.status === "starting" ? "Ox is starting." : "Ox is not running."}
                  {workspace.status === "starting" ? null : (
                    <Button aria-label={`Restart ${workspace.name}`} onClick={() => onRestart(workspace.id)} type="button">Restart</Button>
                  )}
                </p>
              )}
            </li>
          ))}
        </ul>
      ) : <p>Register a server-local workspace to begin.</p>}
      <form onSubmit={(event) => { event.preventDefault(); onRegister(); }}>
        <Label>
          Workspace path
          <Input onChange={(event) => onPath(event.target.value)} required value={path} />
        </Label>
        <Button type="submit">Register workspace</Button>
      </form>
      {message ? <p aria-live="polite" role={message.error ? "alert" : undefined}>{message.text}</p> : null}
    </nav>
  );
}
