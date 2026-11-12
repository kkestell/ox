import { invoke } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";
import { FolderPlus, Plus, X } from "lucide-react";
import { useState } from "react";
import { Dialog } from "./Dialog";
import { NewBranchDialog } from "./NewBranchDialog";
import type { Checkout, Deletion, SessionInfo, SessionState, WorkspaceInfo } from "./protocol";
import { showError } from "./showError";

type Agent = { id: string; name: string };

const agents: [Agent, ...Agent[]] = [{ id: "claude", name: "Claude" }];

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
  // The agent the New Session menus start.
  const [agent, setAgent] = useState(agents[0].id);
  // The workspace and agent the New Worktree on Branch dialog is for.
  const [newBranch, setNewBranch] = useState<{ workspaceId: string; agent: string } | null>(null);
  // The sessions whose deletes are in flight.
  const [deleting, setDeleting] = useState<ReadonlySet<string>>(new Set());
  // A delete that would lose the unsaved work `message` lists.
  const [unsaved, setUnsaved] = useState<{ sessionId: string; message: string } | null>(null);
  const [keptBranch, setKeptBranch] = useState<string | null>(null);

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

  const newSession = (workspaceId: string, agent: string, checkout: Checkout) =>
    run(async () => {
      const session = await invoke<SessionInfo>("new_session", { agent, workspaceId, checkout });
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

  const deleteSession = async (sessionId: string, discard: boolean) => {
    setDeleting((deleting) => new Set(deleting).add(sessionId));
    try {
      const deletion = await invoke<Deletion>("delete_session", { sessionId, discard });
      if (deletion.type === "unsaved_work") {
        setUnsaved({ sessionId, message: deletion.message });
      } else if (deletion.kept_branch !== null) {
        setKeptBranch(deletion.kept_branch);
      }
    } catch (e) {
      await showError(e);
    } finally {
      setDeleting((deleting) => {
        const rest = new Set(deleting);
        rest.delete(sessionId);
        return rest;
      });
    }
  };

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
                  className="menu new-session-menu"
                  role="menu"
                  popover="auto"
                  style={{ positionAnchor: `--new-session-${workspace.id}` }}
                >
                  <select
                    aria-label="Agent"
                    title="Agent"
                    value={agent}
                    onChange={(event) => setAgent(event.target.value)}
                  >
                    {agents.map((choice) => (
                      <option key={choice.id} value={choice.id}>
                        {choice.name}
                      </option>
                    ))}
                  </select>
                  <button
                    type="button"
                    role="menuitem"
                    popoverTarget={`new-session-${workspace.id}`}
                    popoverTargetAction="hide"
                    onClick={() => newSession(workspace.id, agent, { type: "workspace" })}
                  >
                    Existing Checkout
                  </button>
                  {workspace.git_working_tree && (
                    <>
                      <button
                        type="button"
                        role="menuitem"
                        popoverTarget={`new-session-${workspace.id}`}
                        popoverTargetAction="hide"
                        onClick={() =>
                          newSession(workspace.id, agent, {
                            type: "worktree",
                            branch: null,
                            start: null,
                          })
                        }
                      >
                        New Worktree
                      </button>
                      <button
                        type="button"
                        role="menuitem"
                        popoverTarget={`new-session-${workspace.id}`}
                        popoverTargetAction="hide"
                        onClick={() => setNewBranch({ workspaceId: workspace.id, agent })}
                      >
                        New Worktree on Branch…
                      </button>
                    </>
                  )}
                </div>
                <button
                  type="button"
                  className="icon-button"
                  title={
                    workspaceSessions.length > 0
                      ? "Delete the workspace's sessions to remove it"
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
                        title="Delete Session"
                        aria-label="Delete Session"
                        disabled={deleting.has(session.id)}
                        onClick={() => deleteSession(session.id, false)}
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
      {newBranch !== null && (
        <NewBranchDialog
          agent={newBranch.agent}
          workspaceId={newBranch.workspaceId}
          onCreated={(session) => {
            setNewBranch(null);
            onSelect(session.id);
          }}
          onClose={() => setNewBranch(null)}
        />
      )}
      {unsaved !== null && (
        <Dialog
          title="Discard unsaved work?"
          onClose={() => setUnsaved(null)}
          actions={
            <>
              <button type="button" className="text-button" onClick={() => setUnsaved(null)}>
                Cancel
              </button>
              <button
                type="button"
                className="text-button"
                onClick={() => {
                  setUnsaved(null);
                  deleteSession(unsaved.sessionId, true);
                }}
              >
                Discard and Delete
              </button>
            </>
          }
        >
          <p className="message">{unsaved.message}</p>
        </Dialog>
      )}
      {keptBranch !== null && (
        <Dialog
          title="Branch kept"
          onClose={() => setKeptBranch(null)}
          actions={
            <button type="button" className="text-button" onClick={() => setKeptBranch(null)}>
              OK
            </button>
          }
        >
          <p className="message">
            Ox kept the branch <code>{keptBranch}</code>.
          </p>
        </Dialog>
      )}
    </div>
  );
}
