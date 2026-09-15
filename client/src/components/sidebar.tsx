import { type Snapshot } from "../protocol.ts";

// Navigating between conversations is a link, so the sidebar reads as
// navigation; every control that changes host state stays a button.
export function Sidebar({ onAdd, onClose, onNew, onOlder, onOpen, onRemove, onRestart, onSettings, sessions, settings, workspaces }: {
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
    <nav aria-label="Workspaces and conversations" className="sidebar">
      <header>
        <h1>Ox</h1>
        <button aria-label="Add workspace" className="icon" onClick={onAdd} title="Add workspace" type="button"><PlusIcon /></button>
        <button aria-label="Close navigation" className="close icon" onClick={onClose} title="Close navigation" type="button"><CloseIcon /></button>
      </header>
      <div className="scroll">
        {workspaces.values.length > 0 ? (
          <ul aria-label="Workspaces" className="workspaces">
            {workspaces.values.map((workspace) => (
              <li aria-current={workspaces.selectedId === workspace.id ? "true" : undefined} className="workspace" key={workspace.id}>
                <header>
                  <h2 className="truncate">{workspace.name}</h2>
                  <button aria-label={`New conversation in ${workspace.name}`} className="icon" onClick={() => onNew(workspace.id)} title="New conversation" type="button"><PlusIcon /></button>
                  <a
                    aria-current={settings && workspaces.selectedId === workspace.id ? "page" : undefined}
                    aria-label={`Settings for ${workspace.name}`}
                    className="icon"
                    href={`#settings-${workspace.id}`}
                    onClick={() => onSettings(workspace.id)}
                    title="Settings"
                  ><SettingsIcon /></a>
                  <button aria-label={`Remove ${workspace.name}`} className="danger icon" onClick={() => onRemove(workspace.id)} title="Remove workspace" type="button"><TrashIcon /></button>
                </header>
                {workspace.status === "ready" ? (
                  <>
                    <ul aria-label={`${workspace.name} conversations`} className="conversations">
                      {workspace.conversations.map((conversation) => (
                        <li key={conversation.id}>
                          <a
                            aria-current={!settings && sessions.selectedId === conversation.id ? "page" : undefined}
                            href={`#${conversation.id}`}
                            onClick={() => onOpen(workspace.id, conversation.id)}
                          >
                            <span className="truncate">{conversation.title ?? "Untitled conversation"}</span>
                            {conversation.status === "loading" || conversation.awaiting || conversation.updatedAt ? (
                              <span className="meta">
                                {conversation.updatedAt ? <time dateTime={conversation.updatedAt}>{new Date(conversation.updatedAt).toLocaleString()}</time> : null}
                                {conversation.status === "loading" ? <span>Opening…</span> : null}
                                {conversation.awaiting ? <span className="waiting">Waiting for you</span> : null}
                              </span>
                            ) : null}
                          </a>
                        </li>
                      ))}
                    </ul>
                    {workspaces.selectedId === workspace.id && sessions.nextCursor ? <button className="older" onClick={onOlder} type="button">Show older conversations</button> : null}
                  </>
                ) : (
                  <p className="stopped">
                    {workspace.status === "starting" ? "Ox is starting." : "Ox is not running."}
                    {workspace.status === "starting" ? null : <button aria-label={`Restart ${workspace.name}`} className="outline" onClick={() => onRestart(workspace.id)} type="button"><RestartIcon />Restart</button>}
                  </p>
                )}
              </li>
            ))}
          </ul>
        ) : <p className="empty">Register a server-local workspace to begin.</p>}
      </div>
    </nav>
  );
}

function PlusIcon() {
  return <svg aria-hidden="true" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="2" viewBox="0 0 24 24"><path d="M12 5v14M5 12h14" /></svg>;
}

function SettingsIcon() {
  return <svg aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="3" /><path d="M12 2v3M12 19v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M2 12h3M19 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1" /></svg>;
}

function TrashIcon() {
  return <svg aria-hidden="true" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="2" viewBox="0 0 24 24"><path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3" /></svg>;
}

function CloseIcon() {
  return <svg aria-hidden="true" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="2" viewBox="0 0 24 24"><path d="M18 6 6 18M6 6l12 12" /></svg>;
}

function RestartIcon() {
  return <svg aria-hidden="true" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="2" viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6" /></svg>;
}
