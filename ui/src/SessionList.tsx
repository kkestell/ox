import { invoke } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";
import { FolderPlus, Plus, X } from "lucide-react";
import { useState } from "react";
import type { SessionInfo, SessionState, WorkspaceInfo } from "./protocol";

const agents = [
  { id: "claude", name: "Claude" },
  { id: "opencode", name: "OpenCode" },
];

const stateLabels: Record<SessionState, string> = {
  starting: "Starting",
  idle: "Idle",
  running: "Running",
  awaiting_permission: "Awaiting permission",
  exited: "Exited",
};

/** The last component of a workspace's path. */
function directoryName(path: string) {
  return path.split("/").filter(Boolean).pop() ?? path;
}

export function SessionList({
  workspaces,
  sessions,
  selected,
  onSelect,
  error,
  onRetry,
}: {
  workspaces: WorkspaceInfo[];
  sessions: SessionInfo[];
  selected: string | null;
  onSelect: (sessionId: string) => void;
  error: string | null;
  onRetry: () => void;
}) {
  // The id of the workspace whose New Session menu is open.
  const [menuOpen, setMenuOpen] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const run = async (action: () => Promise<unknown>) => {
    try {
      await action();
      setActionError(null);
    } catch (e) {
      setActionError(String(e));
    }
  };

  const addWorkspace = async () => {
    const path = await open({ directory: true, title: "Add Workspace" });
    if (path === null) return;
    await run(() => invoke("add_workspace", { path }));
  };

  const removeWorkspace = (workspaceId: string) =>
    run(() => invoke("remove_workspace", { workspaceId }));

  const newSession = async (workspace: WorkspaceInfo, agent: (typeof agents)[number]) => {
    setMenuOpen(null);
    await run(async () => {
      const session = await invoke<SessionInfo>("new_session", {
        agent: agent.id,
        workspaceId: workspace.id,
      });
      onSelect(session.id);
    });
  };

  // Viewing a saved session whose agent is not running starts its agent, which
  // replays the conversation.
  const select = async (session: SessionInfo) => {
    if (session.state === "exited" && session.agent_session_id !== null) {
      await run(() => invoke("load_session", { sessionId: session.id }));
    }
    onSelect(session.id);
  };

  const close = (sessionId: string) => run(() => invoke("close_session", { sessionId }));

  return (
    <div className="session-list">
      {error && (
        <p className="error">
          {error}{" "}
          <button type="button" onClick={onRetry}>
            Retry
          </button>
        </p>
      )}
      {actionError && <p className="error">{actionError}</p>}
      <div className="sidebar-header">
        <button
          type="button"
          className="add-workspace"
          title="Add Workspace"
          aria-label="Add Workspace"
          onClick={addWorkspace}
        >
          <FolderPlus />
        </button>
      </div>
      {workspaces.map((workspace) => (
        <section
          key={workspace.id}
          className="workspace"
          aria-labelledby={`workspace-${workspace.id}`}
        >
          <div className="workspace-header">
            <h2 id={`workspace-${workspace.id}`} title={workspace.path}>
              {directoryName(workspace.path)}
            </h2>
            <div className="new-session">
              <button
                type="button"
                title="New Session"
                aria-label="New Session"
                aria-expanded={menuOpen === workspace.id}
                onClick={() => setMenuOpen(menuOpen === workspace.id ? null : workspace.id)}
              >
                <Plus />
              </button>
              {menuOpen === workspace.id && (
                <div className="menu" role="menu">
                  {agents.map((agent) => (
                    <button
                      key={agent.id}
                      type="button"
                      role="menuitem"
                      onClick={() => newSession(workspace, agent)}
                    >
                      {agent.name}
                    </button>
                  ))}
                </div>
              )}
            </div>
            <button
              type="button"
              className="remove"
              title="Remove Workspace"
              aria-label="Remove Workspace"
              onClick={() => removeWorkspace(workspace.id)}
            >
              <X />
            </button>
          </div>
          <ul className="sessions">
            {sessions
              .filter((session) => session.workspace_id === workspace.id)
              .map((session) => {
                const state = stateLabels[session.state];
                return (
                  <li key={session.id} className="session">
                    <button
                      type="button"
                      className="select"
                      title={session.cwd}
                      aria-current={session.id === selected}
                      onClick={() => select(session)}
                    >
                      <span
                        className={`status ${session.state}`}
                        role="img"
                        aria-label={state}
                        title={state}
                      />
                      <span className="title">{session.title ?? "New session"}</span>
                    </button>
                    <button
                      type="button"
                      className="close"
                      title="Close Session"
                      aria-label="Close Session"
                      onClick={() => close(session.id)}
                    >
                      <X />
                    </button>
                  </li>
                );
              })}
          </ul>
        </section>
      ))}
    </div>
  );
}
