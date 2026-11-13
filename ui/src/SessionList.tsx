import { invoke } from "@tauri-apps/api/core";
import { Menu } from "@tauri-apps/api/menu";
import { open } from "@tauri-apps/plugin-dialog";
import { Archive, ArchiveRestore, ChevronDown, ChevronRight, FolderPlus, Plus } from "lucide-react";
import { type MouseEvent, useState } from "react";
import { Dialog } from "./Dialog";
import { NewBranchDialog } from "./NewBranchDialog";
import type {
  Archival,
  Checkout,
  Deletion,
  SessionInfo,
  SessionState,
  WorkspaceInfo,
} from "./protocol";
import { showError } from "./showError";

type Agent = { id: string; name: string };

const agents: [Agent, ...Agent[]] = [{ id: "claude", name: "Claude" }];

const stateLabels: Record<SessionState, string> = {
  starting: "Starting",
  idle: "Idle",
  running: "Running",
  awaiting_permission: "Awaiting permission",
  awaiting_input: "Awaiting input",
  exited: "Exited",
};

/** Opens a native context menu of `items` at the pointer. */
async function contextMenu(
  event: MouseEvent,
  items: { text: string; enabled: boolean; action: () => void }[],
) {
  event.preventDefault();
  const menu = await Menu.new({ items });
  await menu.popup();
}

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
  // The sessions whose archives, unarchives or deletes are in flight.
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set());
  // The workspaces whose Archived groups are open.
  const [openArchives, setOpenArchives] = useState<ReadonlySet<string>>(new Set());
  // An archive or delete that would lose the unsaved work `message` lists.
  const [unsaved, setUnsaved] = useState<{
    sessionId: string;
    action: "archive" | "delete";
    message: string;
  } | null>(null);
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
    if (session.state === "exited" && !session.archived && session.agent_session_id !== null) {
      await run(() => invoke("load_session", { sessionId: session.id }));
    }
    onSelect(session.id);
  };

  // Runs `action` with the session's row actions disabled.
  const withBusy = async (sessionId: string, action: () => Promise<void>) => {
    setBusy((busy) => new Set(busy).add(sessionId));
    try {
      await action();
    } catch (e) {
      await showError(e);
    } finally {
      setBusy((busy) => {
        const rest = new Set(busy);
        rest.delete(sessionId);
        return rest;
      });
    }
  };

  const deleteSession = (sessionId: string, discard: boolean) =>
    withBusy(sessionId, async () => {
      const deletion = await invoke<Deletion>("delete_session", { sessionId, discard });
      if (deletion.type === "unsaved_work") {
        setUnsaved({ sessionId, action: "delete", message: deletion.message });
      } else if (deletion.kept_branch !== null) {
        setKeptBranch(deletion.kept_branch);
      }
    });

  const archiveSession = (sessionId: string, discard: boolean) =>
    withBusy(sessionId, async () => {
      const archival = await invoke<Archival>("archive_session", { sessionId, discard });
      if (archival.type === "unsaved_work") {
        setUnsaved({ sessionId, action: "archive", message: archival.message });
      }
    });

  const unarchiveSession = (sessionId: string) =>
    withBusy(sessionId, () => invoke("unarchive_session", { sessionId }));

  const toggleArchive = (workspaceId: string) =>
    setOpenArchives((open) => {
      const next = new Set(open);
      if (!next.delete(workspaceId)) next.add(workspaceId);
      return next;
    });

  const sessionRow = (session: SessionInfo) => {
    const state = stateLabels[session.state];
    return (
      <li
        key={session.id}
        className="session"
        onContextMenu={(event) =>
          contextMenu(event, [
            {
              text: "Delete Session",
              enabled: !busy.has(session.id),
              action: () => deleteSession(session.id, false),
            },
          ])
        }
      >
        <button
          type="button"
          className="select"
          title={session.cwd}
          aria-current={session.id === selected}
          onClick={() => select(session)}
        >
          <span className={`status ${session.state}`} role="img" aria-label={state} title={state} />
          <span className="title">{session.title ?? "New session"}</span>
        </button>
        <div className="row-actions">
          {session.archived ? (
            <button
              type="button"
              className="icon-button"
              title="Unarchive Session"
              aria-label="Unarchive Session"
              disabled={busy.has(session.id)}
              onClick={() => unarchiveSession(session.id)}
            >
              <ArchiveRestore />
            </button>
          ) : (
            <button
              type="button"
              className="icon-button"
              title="Archive Session"
              aria-label="Archive Session"
              disabled={busy.has(session.id)}
              onClick={() => archiveSession(session.id, false)}
            >
              <Archive />
            </button>
          )}
        </div>
      </li>
    );
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
          const active = workspaceSessions.filter((session) => !session.archived);
          const archived = workspaceSessions.filter((session) => session.archived);
          const archiveOpen = openArchives.has(workspace.id);
          return (
            <section
              key={workspace.id}
              className="workspace"
              aria-labelledby={`workspace-${workspace.id}`}
            >
              <div className="workspace-header">
                <h2
                  id={`workspace-${workspace.id}`}
                  title={workspace.path}
                  onContextMenu={(event) =>
                    contextMenu(event, [
                      {
                        text: "Remove Workspace",
                        enabled: workspaceSessions.length === 0,
                        action: () => removeWorkspace(workspace.id),
                      },
                    ])
                  }
                >
                  {directoryName(workspace.path)}
                </h2>
                <button
                  type="button"
                  className="icon-button"
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
              </div>
              <ul className="sessions">
                {active.map(sessionRow)}
                {archived.length > 0 && (
                  <li className="session archived-toggle">
                    <button
                      type="button"
                      className="select"
                      aria-expanded={archiveOpen}
                      onClick={() => toggleArchive(workspace.id)}
                    >
                      <span className="status">
                        {archiveOpen ? <ChevronDown /> : <ChevronRight />}
                      </span>
                      <span className="title">Archived ({archived.length})</span>
                    </button>
                  </li>
                )}
                {archiveOpen && archived.map(sessionRow)}
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
                  if (unsaved.action === "archive") archiveSession(unsaved.sessionId, true);
                  else deleteSession(unsaved.sessionId, true);
                }}
              >
                {unsaved.action === "archive" ? "Discard and Archive" : "Discard and Delete"}
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
