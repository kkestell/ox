import { invoke } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";
import { FolderPlus, Plus, X } from "lucide-react";
import type { SessionInfo, SessionState, WorkspaceInfo } from "./protocol";
import { showError } from "./showError";

const agents = [{ id: "claude", name: "Claude" }];

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
}: {
  workspaces: WorkspaceInfo[];
  sessions: SessionInfo[];
  selected: string | null;
  onSelect: (sessionId: string) => void;
}) {
  const run = async (action: () => Promise<unknown>) => {
    try {
      await action();
    } catch (e) {
      await showError(e);
    }
  };

  const addWorkspace = async () => {
    const path = await open({ directory: true, title: "Add Workspace" });
    if (path === null) return;
    await run(() => invoke("add_workspace", { path }));
  };

  const removeWorkspace = (workspaceId: string) =>
    run(() => invoke("remove_workspace", { workspaceId }));

  const newSession = (workspaceId: string, agent: string) =>
    run(async () => {
      const session = await invoke<SessionInfo>("new_session", { agent, workspaceId });
      onSelect(session.id);
    });

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
    <div className="sidebar">
      <header className="pane-header sidebar-header">
        <button
          type="button"
          className="icon-button"
          title="Add Workspace"
          aria-label="Add Workspace"
          onClick={addWorkspace}
        >
          <FolderPlus />
        </button>
      </header>
      <div className="session-list">
        {workspaces.map((workspace) => {
          const workspaceSessions = sessions.filter(
            (session) => session.workspace_id === workspace.id,
          );
          return (
            <section
              key={workspace.id}
              className="workspace"
              aria-labelledby={`workspace-${workspace.id}`}
            >
              <div className="workspace-header">
                <h2 id={`workspace-${workspace.id}`} title={workspace.path}>
                  {directoryName(workspace.path)}
                </h2>
                <button
                  type="button"
                  className="icon-button new-session"
                  title="New Session"
                  aria-label="New Session"
                  popoverTarget={`new-session-${workspace.id}`}
                  style={{ anchorName: `--new-session-${workspace.id}` }}
                >
                  <Plus />
                </button>
                <div
                  id={`new-session-${workspace.id}`}
                  className="menu"
                  role="menu"
                  popover="auto"
                  style={{ positionAnchor: `--new-session-${workspace.id}` }}
                >
                  {agents.map((agent) => (
                    <button
                      key={agent.id}
                      type="button"
                      role="menuitem"
                      popoverTarget={`new-session-${workspace.id}`}
                      popoverTargetAction="hide"
                      onClick={() => newSession(workspace.id, agent.id)}
                    >
                      {agent.name}
                    </button>
                  ))}
                </div>
                <button
                  type="button"
                  className="icon-button"
                  title={
                    workspaceSessions.length > 0
                      ? "Close the workspace's sessions to remove it"
                      : "Remove Workspace"
                  }
                  aria-label="Remove Workspace"
                  disabled={workspaceSessions.length > 0}
                  onClick={() => removeWorkspace(workspace.id)}
                >
                  <X />
                </button>
              </div>
              <ul className="sessions">
                {workspaceSessions.map((session) => {
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
                        className="icon-button close"
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
          );
        })}
      </div>
    </div>
  );
}
